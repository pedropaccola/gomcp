package engine

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pedropaccola/gomcp/internal/engine/state"
	"golang.org/x/tools/imports"
)

// Mutation layer: declaration-level writes over the same addresses the
// lookup layer reads, organized in semantic sections:
//
//   - Creators      fail if the address already exists; can never destroy.
//   - Editors       fail if the address doesn't exist; delete included.
//   - Refactorings  structure-preserving transformations; refused whenever
//                    preservation cannot be guaranteed.
//
// Pipeline principle: every content mutation is a byte-span splice on a
// file's canonical Src — the AST locates spans but is never re-printed, so
// comments cannot drift. Candidate bytes flow through goimports (the server
// owns the import block) and a reparse before anything is swapped; all
// fallible steps precede the swap, so a verb returning an error means state
// is untouched, and Edit extends that to the whole transaction by working
// on a cloned workspace it discards on failure.
//
// Placement policy for new declarations: const and var groups sit at the
// top (after imports), types after them, functions at the bottom, and
// methods immediately after the last declaration of their receiver group.
//
// The file reads top-down from interface to machinery. The external
// interface is exactly Edit, Flush, Reload, and the Tx verbs; every
// section after Refactorings is internal:
//
//   Transaction         Tx, EditReport, Edit — the write gate.
//   Session             Flush and Reload — the disk boundary.
//   Creators/Editors/Refactorings — the Tx verbs.
//   Pipeline            byte splices, the goimports reload choke point,
//                       import self-repair, use gathering.
//   Placement           where a new declaration lands in a file.
//   Fragments           validation of agent-supplied source.
//   Spans & extraction  locating and cutting declaration bytes.
//   Workspace state     the changed-set and the commit-time recheck.

// ----- Transaction -----

// Tx is a mutable view over a cloned workspace. It embeds View, so every
// lookup composes inside a transaction. Mid-Tx reads are parse-fresh but
// type-stale: type truth returns with the commit-time recheck.
type Tx struct {
	*View
	changed map[RelativePath]bool // paths this transaction touched
}

// touch records paths as changed by this transaction; every verb reports
// its footprint here regardless of prior dirtiness.
func (tx *Tx) touch(paths ...RelativePath) {
	for _, path := range paths {
		tx.changed[path] = true
	}
}

// EditReport is the echo of a committed transaction.
type EditReport struct {
	Changed  []RelativePath // files created, modified, moved, or deleted by this Tx
	Delta    []Diagnostic   // diagnostics introduced by this transaction
	Resolved []Diagnostic   // pre-existing diagnostics this transaction fixed
	Stale    bool           // recheck failed: state applied, Delta unavailable
	Note     string         // human-readable recheck failure, when Stale
}

// Edit runs fn against a cloned workspace and commits it with a full
// recheck, mirroring Read. fn returning an error discards every change:
// error means nothing happened. Post-change problems are never errors —
// they arrive as the report's diagnostics delta, because broken code is a
// valid state. If the recheck itself fails, the edit stays applied and the
// report says diagnostics are stale; a valid edit is never rolled back over
// a tooling hiccup.
func (e *Engine) Edit(ctx context.Context, fn func(*Tx) error) (*EditReport, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	orig := e.ws
	e.ws = orig.Clone()

	view := &View{eng: e}
	beforeDiags := make(map[string]Diagnostic)
	for _, diag := range view.AllDiagnostics() {
		beforeDiags[diag.String()] = diag
	}

	tx := &Tx{View: view, changed: make(map[RelativePath]bool)}
	if err := fn(tx); err != nil {
		e.ws = orig
		return nil, err
	}

	stale := func(err error) *EditReport {
		return &EditReport{Changed: sortedKeys(tx.changed), Stale: true, Note: err.Error()}
	}
	if err := e.recheckLocked(ctx); err != nil {
		return stale(err), nil
	}
	// One bounded self-repair pass for imports goimports cannot see, then
	// re-check to fold the repairs into the echo. Best-effort: it can never
	// fail the already-committed edit.
	if tx.repairMissingImports() {
		if err := e.recheckLocked(ctx); err != nil {
			return stale(err), nil
		}
	}

	report := &EditReport{Changed: sortedKeys(tx.changed)}
	after := make(map[string]bool)
	for _, diag := range view.AllDiagnostics() {
		after[diag.String()] = true
		if _, existed := beforeDiags[diag.String()]; !existed {
			report.Delta = append(report.Delta, diag)
		}
	}
	for _, key := range sortedKeys(beforeDiags) {
		if !after[key] {
			report.Resolved = append(report.Resolved, beforeDiags[key])
		}
	}
	return report, nil
}

// ----- Session -----

// Flush writes every dirty file to disk, unlinks tombstoned paths, and
// clears both marks — the only place the mutation layer touches the
// filesystem.
func (e *Engine) Flush() (written, removed []RelativePath, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, addr := range e.ws.UnitKeys() {
		unit, _ := e.ws.Unit(addr)
		for _, pkg := range []*Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if !file.Dirty() {
					continue
				}
				abs := e.absPath(file.Path)
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return written, removed, err
				}
				if err := os.WriteFile(abs, file.Src(), 0o644); err != nil {
					return written, removed, err
				}
				file.MarkFlushed()
				written = append(written, file.Path)
			}
		}
	}
	for _, path := range e.ws.Tombstones() {
		if err := os.Remove(e.absPath(path)); err != nil && !os.IsNotExist(err) {
			return written, removed, err
		}
		e.ws.ClearTombstone(path)
		removed = append(removed, path)
	}
	return written, removed, nil
}

// Reload rebuilds the workspace from disk, discarding every unflushed
// change: the disk-facing inverse of Flush. It reports the files whose
// in-memory state was lost — dirty files and pending removals. An error
// means the previous state is untouched.
func (e *Engine) Reload(ctx context.Context) ([]RelativePath, error) {
	e.mu.RLock()
	var discarded []RelativePath
	for _, addr := range e.ws.UnitKeys() {
		unit, _ := e.ws.Unit(addr)
		for _, pkg := range []*Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if file.Dirty() {
					discarded = append(discarded, file.Path)
				}
			}
		}
	}
	discarded = append(discarded, e.ws.Tombstones()...)
	e.mu.RUnlock()
	slices.Sort(discarded)
	discarded = slices.Compact(discarded)
	if err := e.Bootstrap(ctx); err != nil {
		return nil, err
	}
	return discarded, nil
}

// ----- Creators -----

// CreatePackage creates a new package at a module-prefixed address with one
// file named after the package. name defaults to the address base. Fails if
// the address already holds a package; the directory is created at Flush.
func (tx *Tx) CreatePackage(pkg PkgPath, name string) error {
	dir, ok := tx.eng.dirOf(pkg)
	if !ok || dir == "." || dir.EscapesRoot() {
		return fmt.Errorf("cannot create a package at %q: workspace packages live under module %q", pkg, tx.eng.ws.Module())
	}
	if _, exists := tx.eng.ws.Unit(pkg); exists {
		return fmt.Errorf("a package already exists at %q", pkg)
	}
	if name == "" {
		name = filepath.Base(string(dir))
	}
	if !token.IsIdentifier(name) {
		return fmt.Errorf("%q is not a valid package name", name)
	}
	p := &Package{
		Name:    name,
		Path:    dir,
		PkgPath: pkg,
	}
	if err := tx.reloadFile(p, dir.Join(name+".go"), []byte("package "+name+"\n")); err != nil {
		return err
	}
	tx.eng.ws.InstallUnit(pkg, &Unit{Prod: p})
	return nil
}

// CreateFile adds an empty file to an existing package.
func (tx *Tx) CreateFile(pkg PkgPath, name string) error {
	p, ok := tx.Package(pkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", pkg)
	}
	path, err := fileAddress(p, name)
	if err != nil {
		return err
	}
	if _, _, exists := tx.File(path); exists {
		return fmt.Errorf("file %q already exists", path)
	}
	return tx.reloadFile(p, path, []byte("package "+p.Name+"\n"))
}

// CreateSymbol adds one new top-level declaration to a file of an existing
// package, at its canonical position. The file is required, never inferred —
// but a missing file inside the package is created implicitly, since
// creation cannot destroy.
func (tx *Tx) CreateSymbol(pkg PkgPath, fileName, src string) error {
	p, ok := tx.Package(pkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", pkg)
	}
	frag, err := parseDeclFragment(src)
	if err != nil {
		return err
	}
	for _, key := range frag.keys {
		if key == "init" {
			continue // any number of init functions is legal
		}
		if _, exists := p.Symbol(key); exists {
			return fmt.Errorf("symbol %q already exists in %q: use ReplaceSymbol", key, pkg)
		}
	}
	path, err := fileAddress(p, fileName)
	if err != nil {
		return err
	}
	file, ok := p.File(path)
	if !ok {
		candidate := []byte("package " + p.Name + "\n\n" + src + "\n")
		return tx.reloadFile(p, path, candidate)
	}
	at := tx.insertOffset(file, frag)
	return tx.reloadFile(p, path, applySplices(file.Src(), []splice{{span: span{start: at, end: at}, repl: []byte("\n\n" + src + "\n")}}))
}

// ----- Editors -----

// ReplaceSymbol replaces key's whole declaration with src — for members of
// grouped declarations, src is the member's spec as written inside the
// group. A replacement may rename; the new key must not collide.
func (tx *Tx) ReplaceSymbol(pkg PkgPath, key, src string) error {
	sym, owner, ok := tx.Symbol(pkg, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	var frag fragment
	var sp span
	var spanOK bool
	var err error
	if gen, grouped := groupOf(sym); grouped {
		frag, err = parseSpecFragment(gen.Tok, src)
		sp, spanOK = tx.specSpan(sym)
	} else {
		frag, err = parseDeclFragment(src)
		sp, spanOK = tx.declSpan(sym)
	}
	if err != nil {
		return err
	}
	if !spanOK {
		return fmt.Errorf("cannot locate %q in source", key)
	}
	for _, newKey := range frag.keys {
		if newKey == key || newKey == "init" {
			continue
		}
		if _, exists := owner.Symbol(newKey); exists {
			return fmt.Errorf("replacement declares %q, which already exists in %q", newKey, pkg)
		}
	}
	file, _ := owner.File(sym.File)
	return tx.reloadFile(owner, sym.File, applySplices(file.Src(), []splice{{span: sp, repl: []byte(src)}}))
}

// DeleteSymbol removes key's declaration — its spec alone when it lives in
// a grouped declaration with siblings.
func (tx *Tx) DeleteSymbol(pkg PkgPath, key string) error {
	sym, owner, ok := tx.Symbol(pkg, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	if spec, ok := sym.Spec.(*ast.ValueSpec); ok && len(spec.Names) > 1 {
		return fmt.Errorf("%q is declared together with other names: replace the spec instead", key)
	}
	sp, ok := tx.declSpan(sym)
	if gen, grouped := groupOf(sym); grouped && len(gen.Specs) > 1 {
		sp, ok = tx.specSpan(sym)
	}
	if !ok {
		return fmt.Errorf("cannot locate %q in source", key)
	}
	file, _ := owner.File(sym.File)
	return tx.reloadFile(owner, sym.File, applySplices(file.Src(), []splice{{span: sp}}))
}

// DeleteFile removes one file and every declaration in it, tombstoning the
// path for Flush.
func (tx *Tx) DeleteFile(pkg PkgPath, name string) error {
	unit, ok := tx.eng.ws.Unit(pkg)
	if !ok {
		return fmt.Errorf("no package at %q", pkg)
	}
	for _, owner := range []*Package{unit.Prod, unit.XTest} {
		if owner == nil {
			continue
		}
		path, err := fileAddress(owner, name)
		if err != nil {
			return err
		}
		if _, ok := owner.File(path); !ok {
			continue
		}
		tx.eng.ws.DropFile(pkg, owner, path)
		tx.touch(path)
		return nil
	}
	return fmt.Errorf("no file %q in %q", name, pkg)
}

// DeletePackage removes a whole package address, tombstoning every file.
func (tx *Tx) DeletePackage(pkg PkgPath) error {
	unit, ok := tx.eng.ws.Unit(pkg)
	if !ok {
		return fmt.Errorf("no package at %q", pkg)
	}
	for _, p := range []*Package{unit.Prod, unit.XTest} {
		if p == nil {
			continue
		}
		for _, file := range p.Files() {
			tx.eng.ws.Tombstone(file.Path, p.Name)
			tx.touch(file.Path)
		}
	}
	tx.eng.ws.RemoveUnit(pkg)
	return nil
}

// ----- Refactorings -----

// MoveSymbol relocates key's declaration to another file of the same
// package: a pure splice, no reference is touched. A member of a grouped
// declaration is extracted as a standalone declaration; extraction refuses
// members whose meaning depends on their position in the group. Moves
// never cross the test build boundary, and the destination file is created
// when missing.
func (tx *Tx) MoveSymbol(pkg PkgPath, key, fileName string) error {
	sym, owner, ok := tx.Symbol(pkg, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	destPath, err := fileAddress(owner, fileName)
	if err != nil {
		return err
	}
	if destPath == sym.File {
		return fmt.Errorf("%q already lives in %q", key, destPath)
	}
	if strings.HasSuffix(fileName, "_test.go") != strings.HasSuffix(sym.File.String(), "_test.go") {
		return fmt.Errorf("moving %q from %q to %q would cross the test build boundary", key, sym.File, destPath)
	}
	file, _ := owner.File(sym.File)
	src, sp, err := tx.extractDecl(sym, file)
	if err != nil {
		return err
	}
	frag, err := parseDeclFragment(src)
	if err != nil {
		return err
	}
	dest, inOwner := owner.File(destPath)
	if _, _, exists := tx.File(destPath); exists && !inOwner {
		return fmt.Errorf("file %q belongs to another package", destPath)
	}
	if err := tx.reloadFile(owner, sym.File, applySplices(file.Src(), []splice{{span: sp}})); err != nil {
		return err
	}
	if !inOwner {
		return tx.reloadFile(owner, destPath, []byte("package "+owner.Name+"\n\n"+src+"\n"))
	}
	at := tx.insertOffset(dest, frag)
	return tx.reloadFile(owner, destPath, applySplices(dest.Src(), []splice{{span: span{start: at, end: at}, repl: []byte("\n\n" + src + "\n")}}))
}

// RenameSymbol renames key to newName everywhere: the defining identifier
// and every resolved use across the workspace, matched by qualified name.
// v1 renames exactly this one object — renaming an interface method does
// not chase implementors; broken satisfactions arrive in the echo instead.
func (tx *Tx) RenameSymbol(pkg PkgPath, key, newName string) error {
	if !token.IsIdentifier(newName) {
		return fmt.Errorf("%q is not a valid identifier", newName)
	}
	sym, owner, ok := tx.Symbol(pkg, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	newKey := newName
	if sym.Kind == KindMethod {
		newKey = sym.Recv + "." + newName
	}
	if _, exists := owner.Symbol(newKey); exists {
		return fmt.Errorf("symbol %q already exists in %q", newKey, pkg)
	}
	target := objKey(tx.objectOf(sym))
	if target == "" {
		return fmt.Errorf("type information unavailable for %q", key)
	}

	edits := make(map[RelativePath][]splice)
	def := definingIdent(sym)
	if sp, ok := tx.offsetSpan(sym.File, def.Pos(), def.End()); ok {
		edits[sym.File] = append(edits[sym.File], splice{span: sp, repl: []byte(newName)})
	}
	tx.gatherUses(target, func(relFile RelativePath, sp span) {
		edits[relFile] = append(edits[relFile], splice{span: sp, repl: []byte(newName)})
	})
	return tx.applyFileSplices(edits)
}

// RenameFile renames a file within its package — semantically free in Go,
// files are storage. The old path is tombstoned for Flush.
func (tx *Tx) RenameFile(pkg PkgPath, name, newName string) error {
	unit, ok := tx.eng.ws.Unit(pkg)
	if !ok {
		return fmt.Errorf("no package at %q", pkg)
	}
	for _, owner := range []*Package{unit.Prod, unit.XTest} {
		if owner == nil {
			continue
		}
		path, err := fileAddress(owner, name)
		if err != nil {
			return err
		}
		if _, ok := owner.File(path); !ok {
			continue
		}
		newPath, err := fileAddress(owner, newName)
		if err != nil {
			return err
		}
		if _, _, exists := tx.File(newPath); exists {
			return fmt.Errorf("file %q already exists", newPath)
		}
		tx.eng.ws.MoveFile(owner, path, newPath)
		tx.touch(path, newPath)
		return nil
	}
	return fmt.Errorf("no file %q in %q", name, pkg)
}

// RenamePackage moves a package to a new address, rewriting the import
// path in every importer. When the package name equals the old address
// base (the convention), the package clause and every unaliased qualifier
// are renamed too; aliased imports keep their alias untouched.
func (tx *Tx) RenamePackage(oldPkg, newPkg PkgPath) error {
	dir, ok := tx.eng.dirOf(oldPkg)
	if !ok || dir == "." {
		return fmt.Errorf("no workspace package at %q", oldPkg)
	}
	newDir, ok := tx.eng.dirOf(newPkg)
	if !ok || newDir == "." || newDir.EscapesRoot() {
		return fmt.Errorf("cannot rename %q to %q: workspace packages live under module %q", oldPkg, newPkg, tx.eng.ws.Module())
	}
	unit, ok := tx.eng.ws.Unit(oldPkg)
	if !ok {
		return fmt.Errorf("no package at %q", oldPkg)
	}
	if _, exists := tx.eng.ws.Unit(newPkg); exists {
		return fmt.Errorf("a package already exists at %q", newPkg)
	}
	oldBase, newBase := filepath.Base(string(dir)), filepath.Base(string(newDir))
	renameName := unit.Prod != nil && unit.Prod.Name == oldBase && oldBase != newBase
	if renameName && !token.IsIdentifier(newBase) {
		return fmt.Errorf("%q is not a valid package name", newBase)
	}
	oldImport, newImport := string(oldPkg), string(newPkg)

	// Importers first: their files are disjoint from the moving package's.
	// The unit's own XTest package is an importer too — it imports its
	// production sibling — so only Prod itself is skipped.
	edits := make(map[RelativePath][]splice)
	for _, pkg := range tx.Packages() {
		if pkg == unit.Prod {
			continue
		}
		for _, file := range pkg.Files() {
			for _, imp := range file.Ast().Imports {
				if imp.Path.Value != strconv.Quote(oldImport) {
					continue
				}
				if sp, ok := tx.offsetSpan(file.Path, imp.Path.Pos(), imp.Path.End()); ok {
					edits[file.Path] = append(edits[file.Path], splice{span: sp, repl: []byte(strconv.Quote(newImport))})
				}
			}
		}
		if renameName && pkg.TypesInfo != nil {
			for ident, obj := range pkg.TypesInfo.Uses {
				pkgName, ok := obj.(*types.PkgName)
				if !ok || pkgName.Imported() == nil || pkgName.Imported().Path() != oldImport {
					continue
				}
				if ident.Name != oldBase {
					continue // aliased import: the alias survives the move
				}
				relFile, err := tx.eng.relativePath(tx.eng.ws.FileSet().Position(ident.Pos()).Filename)
				if err != nil || relFile.EscapesRoot() {
					continue
				}
				if sp, ok := tx.offsetSpan(relFile, ident.Pos(), ident.End()); ok {
					edits[relFile] = append(edits[relFile], splice{span: sp, repl: []byte(newBase)})
				}
			}
		}
	}
	if err := tx.applyFileSplices(edits); err != nil {
		return err
	}

	// Move the address's packages, renaming package clauses when due.
	// Every moved file re-enters through the content pipeline, so SwapFile
	// stays the one door for file content.
	newUnit := &Unit{}
	for i, pkg := range []*Package{unit.Prod, unit.XTest} {
		if pkg == nil {
			continue
		}
		moved := pkg.CloneShell()
		moved.Path = newDir
		moved.PkgPath = PkgPath(strings.Replace(string(pkg.PkgPath), oldImport, newImport, 1))
		if renameName {
			moved.Name = newBase + strings.TrimPrefix(pkg.Name, oldBase)
		}
		for _, file := range pkg.Files() {
			newPath := newDir.Join(filepath.Base(string(file.Path)))
			tx.eng.ws.Tombstone(file.Path, pkg.Name)
			tx.eng.ws.ClearTombstone(newPath)
			tx.touch(file.Path, newPath)
			candidate := file.Src()
			if renameName {
				sp, ok := tx.offsetSpan(file.Path, file.Ast().Name.Pos(), file.Ast().Name.End())
				if !ok {
					return fmt.Errorf("cannot locate package clause of %q", file.Path)
				}
				candidate = applySplices(file.Src(), []splice{{span: sp, repl: []byte(moved.Name)}})
			}
			if err := tx.reloadFile(moved, newPath, candidate); err != nil {
				return err
			}
		}
		if i == 0 {
			newUnit.Prod = moved
		} else {
			newUnit.XTest = moved
		}
	}
	tx.eng.ws.RemoveUnit(oldPkg)
	tx.eng.ws.InstallUnit(newPkg, newUnit)
	return nil
}

// ----- Pipeline -----

// splice is one byte-span edit: replace span with repl (nil deletes).
type splice struct {
	span
	repl []byte
}

// applySplices applies every splice to src in descending offset order so
// earlier spans stay valid.
func applySplices(src []byte, splices []splice) []byte {
	slices.SortFunc(splices, func(a, b splice) int { return cmp.Compare(b.start, a.start) })
	out := slices.Clone(src)
	for _, s := range splices {
		out = slices.Concat(out[:s.start], s.repl, out[s.end:])
	}
	return out
}

// reloadFile is the goimports half of the content pipeline: format the
// candidate bytes, then hand them to the workspace's parse-enforcing
// SwapFile — the one door through which file content enters the model.
// Every fallible step precedes the swap; an error means state is
// untouched.
func (tx *Tx) reloadFile(pkg *Package, path RelativePath, candidate []byte) error {
	abs := tx.eng.absPath(path)
	formatted, err := imports.Process(abs, candidate, nil)
	if err != nil {
		return fmt.Errorf("%s does not format: %w", path, err)
	}
	if err := tx.eng.ws.SwapFile(pkg, path, abs, formatted); err != nil {
		return err
	}
	tx.touch(path)
	return nil
}

// applyFileSplices applies per-file splice batches and reloads each touched
// file, deduplicating overlapping gathers.
func (tx *Tx) applyFileSplices(splices map[RelativePath][]splice) error {
	for _, path := range sortedKeys(splices) {
		file, owner, ok := tx.File(path)
		if !ok {
			return fmt.Errorf("cannot resolve %q while applying splices", path)
		}
		batch := splices[path]
		slices.SortFunc(batch, func(a, b splice) int { return cmp.Compare(a.start, b.start) })
		batch = slices.CompactFunc(batch, func(a, b splice) bool { return a.span == b.span })
		if err := tx.reloadFile(owner, path, applySplices(file.Src(), batch)); err != nil {
			return err
		}
	}
	return nil
}

// repairMissingImports is the bounded self-repair pass behind Edit: when a
// recheck reports "undefined: X" and X names exactly one in-memory package,
// the missing import is spliced in. goimports cannot discover packages that
// exist only in memory (it scans disk), and imports are not
// agent-addressable, so the server must cover its own blind spot.
// Best-effort by design: ambiguous names and failed splices leave the
// diagnostic standing, and a wrong repair (an ident that merely collides
// with a package name) surfaces as an ordinary diagnostic on the next echo
// while goimports drops the then-unused import on the file's next reload.
func (tx *Tx) repairMissingImports() bool {
	// Unique importable package names known to the workspace.
	candidates := make(map[string]PkgPath) // package name -> import path
	ambiguous := make(map[string]bool)
	for _, addr := range tx.eng.ws.UnitKeys() {
		unit, _ := tx.eng.ws.Unit(addr)
		pkg := unit.Prod
		if pkg == nil || pkg.PkgPath == "" || pkg.Name == "main" {
			continue
		}
		if _, dup := candidates[pkg.Name]; dup {
			ambiguous[pkg.Name] = true
			delete(candidates, pkg.Name)
			continue
		}
		candidates[pkg.Name] = pkg.PkgPath
	}

	needed := make(map[RelativePath]map[string]bool) // file -> import paths
	for _, diag := range tx.AllDiagnostics() {
		if diag.Kind != DiagType || diag.File == "" {
			continue
		}
		name, found := strings.CutPrefix(diag.Msg, "undefined: ")
		if !found || !token.IsIdentifier(name) || ambiguous[name] {
			continue
		}
		path, ok := candidates[name]
		if !ok {
			continue
		}
		file, owner, ok := tx.File(diag.File)
		if !ok || owner.PkgPath == path || importsPath(file.Ast(), string(path)) {
			continue
		}
		if needed[diag.File] == nil {
			needed[diag.File] = make(map[string]bool)
		}
		needed[diag.File][string(path)] = true
	}

	repaired := false
	for _, filePath := range sortedKeys(needed) {
		file, owner, ok := tx.File(filePath)
		if !ok {
			continue
		}
		sp, ok := tx.offsetSpan(filePath, file.Ast().Name.Pos(), file.Ast().Name.End())
		if !ok {
			continue
		}
		var repl strings.Builder
		for _, path := range sortedKeys(needed[filePath]) {
			fmt.Fprintf(&repl, "\n\nimport %q", path)
		}
		candidate := applySplices(file.Src(), []splice{{span: span{start: sp.end, end: sp.end}, repl: []byte(repl.String())}})
		if err := tx.reloadFile(owner, filePath, candidate); err != nil {
			continue // repair is best-effort; the diagnostic stays visible
		}
		repaired = true
	}
	return repaired
}

// importsPath reports whether the file already imports path, so the import
// self-repair never splices a duplicate.
func importsPath(astFile *ast.File, path string) bool {
	for _, imp := range astFile.Imports {
		if imp.Path.Value == strconv.Quote(path) {
			return true
		}
	}
	return false
}

// gatherUses walks every package's resolved uses matching the qualified
// target key and hands each identifier's file and span to fn.
func (tx *Tx) gatherUses(target string, fn func(RelativePath, span)) {
	for _, pkg := range tx.Packages() {
		if pkg.TypesInfo == nil {
			continue
		}
		for ident, obj := range pkg.TypesInfo.Uses {
			if objKey(obj) != target {
				continue
			}
			relFile, err := tx.eng.relativePath(tx.eng.ws.FileSet().Position(ident.Pos()).Filename)
			if err != nil || relFile.EscapesRoot() {
				continue
			}
			if sp, ok := tx.offsetSpan(relFile, ident.Pos(), ident.End()); ok {
				fn(relFile, sp)
			}
		}
	}
}

// ----- Placement -----

// insertOffset returns the canonical insertion offset for a new declaration
// per the placement policy: const/var at the top after imports, types after
// values, funcs at the bottom, methods right after their receiver group. A
// method whose receiver group isn't in this file falls to the bottom.
func (tx *Tx) insertOffset(file *File, frag fragment) int {
	effective := frag
	if frag.kind == KindMethod && !hasReceiverAnchor(file.Ast(), frag.recv) {
		effective = fragment{kind: KindFunc}
	}
	var anchor ast.Decl
	for _, decl := range file.Ast().Decls {
		if declPrecedes(decl, effective) {
			anchor = decl
		}
	}
	if anchor == nil {
		// Nothing precedes: insert right after the package clause.
		if sp, ok := tx.offsetSpan(file.Path, file.Ast().Name.Pos(), file.Ast().Name.End()); ok {
			return sp.end
		}
		return len(file.Src())
	}
	if sp, ok := tx.offsetSpan(file.Path, anchor.Pos(), anchor.End()); ok {
		return sp.end
	}
	return len(file.Src())
}

// declPrecedes reports whether decl belongs at or before the fragment's
// canonical region. Regions rank imports < values < types-with-their-methods
// < funcs; a method fragment anchors only to its own receiver group.
func declPrecedes(decl ast.Decl, frag fragment) bool {
	if frag.kind == KindMethod {
		switch d := decl.(type) {
		case *ast.GenDecl:
			return d.Tok == token.IMPORT || d.Tok == token.CONST || d.Tok == token.VAR ||
				(d.Tok == token.TYPE && declaresType(d, frag.recv))
		case *ast.FuncDecl:
			return d.Recv != nil && state.RecvTypeName(d.Recv) == frag.recv
		}
		return false
	}
	return declRegion(decl) <= kindRegion(frag.kind)
}

// declRegion places an existing declaration in the file's canonical region
// order: imports (0) < const/var (1) < types and their methods (2) < plain
// funcs (3). declPrecedes compares it against kindRegion.
func declRegion(decl ast.Decl) int {
	switch d := decl.(type) {
	case *ast.GenDecl:
		switch d.Tok {
		case token.IMPORT:
			return 0
		case token.CONST, token.VAR:
			return 1
		case token.TYPE:
			return 2
		}
	case *ast.FuncDecl:
		if d.Recv != nil {
			return 2 // methods rank with the type region, after their receiver
		}
		return 3
	}
	return 3
}

// kindRegion places a new fragment in the same region order declRegion
// uses for existing declarations; methods never reach it (they anchor to
// their receiver group instead).
func kindRegion(kind SymbolKind) int {
	switch kind {
	case KindConst, KindVar:
		return 1
	case KindType:
		return 2
	default:
		return 3
	}
}

// hasReceiverAnchor reports whether the file declares recv's type or any of
// its methods — the anchor a new method is placed after; without one the
// method falls to the plain-func region at the bottom.
func hasReceiverAnchor(astFile *ast.File, recv string) bool {
	for _, decl := range astFile.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok == token.TYPE && declaresType(d, recv) {
				return true
			}
		case *ast.FuncDecl:
			if d.Recv != nil && state.RecvTypeName(d.Recv) == recv {
				return true
			}
		}
	}
	return false
}

// declaresType reports whether the type declaration declares name, grouped
// or not.
func declaresType(gen *ast.GenDecl, name string) bool {
	for _, spec := range gen.Specs {
		if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == name {
			return true
		}
	}
	return false
}

// ----- Fragments -----

// fragment is a validated piece of agent-supplied source: the symbol keys
// it declares and its placement classification.
type fragment struct {
	keys []string
	kind SymbolKind
	recv string
}

// parseDeclFragment validates src as exactly one top-level declaration.
func parseDeclFragment(src string) (fragment, error) {
	astFile, err := parser.ParseFile(token.NewFileSet(), "fragment.go", "package p\n\n"+src+"\n", parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fragment{}, fmt.Errorf("declaration does not parse: %w", err)
	}
	if len(astFile.Decls) != 1 {
		return fragment{}, fmt.Errorf("expected exactly one top-level declaration, got %d", len(astFile.Decls))
	}
	if gen, ok := astFile.Decls[0].(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
		return fragment{}, fmt.Errorf("import declarations are managed by the server")
	}
	return classifyFragment(astFile), nil
}

// parseSpecFragment validates src as one or more specs of a grouped
// declaration with the given keyword.
func parseSpecFragment(tok token.Token, src string) (fragment, error) {
	wrapped := fmt.Sprintf("package p\n\n%s (\n%s\n)\n", tok, src)
	astFile, err := parser.ParseFile(token.NewFileSet(), "fragment.go", wrapped, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fragment{}, fmt.Errorf("spec does not parse inside a %s group: %w", tok, err)
	}
	return classifyFragment(astFile), nil
}

// classifyFragment derives keys and placement class by reusing the same
// indexer that builds the real symbol tables.
func classifyFragment(astFile *ast.File) fragment {
	symbols := make(map[string]*Symbol)
	inits := state.IndexAST("fragment.go", astFile, symbols)
	frag := fragment{keys: sortedKeys(symbols)}
	for _, key := range frag.keys {
		frag.kind = symbols[key].Kind
		frag.recv = symbols[key].Recv
	}
	for range inits {
		frag.keys = append(frag.keys, "init")
		frag.kind = KindFunc
	}
	return frag
}

// ----- Spans & extraction -----

// declSpan is the byte span of the whole declaration, doc comment included.
func (v *View) declSpan(sym *Symbol) (span, bool) {
	start := sym.Decl.Pos()
	if doc := state.DocOf(sym.Decl); doc != nil {
		start = doc.Pos()
	}
	return v.offsetSpan(sym.File, start, sym.Decl.End())
}

// specSpan is the byte span of the symbol's own spec, doc included.
func (v *View) specSpan(sym *Symbol) (span, bool) {
	if sym.Spec == nil {
		return v.declSpan(sym)
	}
	start := sym.Spec.Pos()
	if doc := state.DocOf(sym.Spec); doc != nil {
		start = doc.Pos()
	}
	return v.offsetSpan(sym.File, start, sym.Spec.End())
}

// extractDecl returns sym's declaration as standalone source together with
// the span its removal splices out, doc comment included in both. A member
// of a grouped declaration with siblings is rebuilt ungrouped — doc first,
// then the group's keyword before the spec. Extraction refuses members
// whose meaning depends on their surroundings: names sharing a spec, and
// const-group values taken from their position (iota, inherited values).
func (tx *Tx) extractDecl(sym *Symbol, file *File) (string, span, error) {
	if spec, ok := sym.Spec.(*ast.ValueSpec); ok && len(spec.Names) > 1 {
		return "", span{}, fmt.Errorf("%q is declared together with other names: replace the spec instead", sym.Key())
	}
	gen, grouped := groupOf(sym)
	if !grouped || len(gen.Specs) == 1 {
		sp, ok := tx.declSpan(sym)
		if !ok {
			return "", span{}, fmt.Errorf("cannot locate %q in source", sym.Key())
		}
		return string(file.Src()[sp.start:sp.end]), sp, nil
	}
	if spec, ok := sym.Spec.(*ast.ValueSpec); ok && gen.Tok == token.CONST && (len(spec.Values) == 0 || groupUsesIota(gen)) {
		return "", span{}, fmt.Errorf("%q takes its value from its position in a const group: move refused", sym.Key())
	}
	sp, ok := tx.specSpan(sym)
	if !ok {
		return "", span{}, fmt.Errorf("cannot locate %q in source", sym.Key())
	}
	body, ok := tx.offsetSpan(sym.File, sym.Spec.Pos(), sym.Spec.End())
	if !ok {
		return "", span{}, fmt.Errorf("cannot locate %q in source", sym.Key())
	}
	doc := string(file.Src()[sp.start:body.start])
	return doc + gen.Tok.String() + " " + string(file.Src()[body.start:body.end]), sp, nil
}

// groupUsesIota reports whether any value expression in a grouped
// declaration references iota, making member meaning position-dependent.
func groupUsesIota(gen *ast.GenDecl) bool {
	found := false
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, value := range vs.Values {
			ast.Inspect(value, func(n ast.Node) bool {
				if ident, ok := n.(*ast.Ident); ok && ident.Name == "iota" {
					found = true
				}
				return !found
			})
		}
	}
	return found
}

// ----- Workspace state -----

// changedSet is the union of dirty files and tombstoned paths.
func (e *Engine) changedSet() map[RelativePath]bool {
	out := make(map[RelativePath]bool)
	for _, addr := range e.ws.UnitKeys() {
		unit, _ := e.ws.Unit(addr)
		for _, pkg := range []*Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if file.Dirty() {
					out[file.Path] = true
				}
			}
		}
	}
	for _, path := range e.ws.Tombstones() {
		out[path] = true
	}
	return out
}

// recheckLocked reloads the workspace with the in-memory truth overlaid on
// disk and swaps the fresh state in, carrying dirty marks over and pruning
// tombstoned paths. Caller must hold the write lock.
func (e *Engine) recheckLocked(ctx context.Context) error {
	overlay := make(map[string][]byte)
	dirty := make(map[RelativePath]bool)
	for path := range e.changedSet() {
		if mask, tombstoned := e.ws.Tombstoned(path); tombstoned {
			overlay[e.absPath(path)] = mask
			continue
		}
		if file, _, ok := (&View{eng: e}).File(path); ok {
			overlay[e.absPath(path)] = file.Src()
			dirty[path] = true
		}
	}

	fset, _, units, err := e.load(ctx, overlay)
	if err != nil {
		return err
	}
	for _, path := range e.ws.Tombstones() {
		state.PruneFile(units, e.pkgAt(path.Dir()), path)
	}
	for path := range dirty {
		if unit, ok := units[e.pkgAt(path.Dir())]; ok {
			unit.MarkDirty(path)
		}
	}
	e.ws.SwapLoaded(fset, units)
	return nil
}

// fileAddress validates a bare *.go name inside pkg's directory.
func fileAddress(pkg *Package, name string) (RelativePath, error) {
	if name == "" || name != filepath.Base(name) || !strings.HasSuffix(name, ".go") {
		return "", fmt.Errorf("file name must be a bare *.go name, got %q", name)
	}
	return pkg.Path.Join(name), nil
}

// tombstone is the overlay mask for a removed file: syntactically valid,
// semantically empty, so rechecks see the deletion's blast radius.
func tombstone(pkgName string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "package %s\n", pkgName)
	return buf.Bytes()
}
