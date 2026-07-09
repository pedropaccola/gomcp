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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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

	origUnits, origRemoved := e.Packages, e.removed
	e.Packages = cloneUnits(origUnits)
	e.removed = maps.Clone(origRemoved)

	view := &View{eng: e}
	beforeDiags := make(map[string]Diagnostic)
	for _, diag := range view.AllDiagnostics() {
		beforeDiags[diag.String()] = diag
	}

	tx := &Tx{View: view, changed: make(map[RelativePath]bool)}
	if err := fn(tx); err != nil {
		e.Packages, e.removed = origUnits, origRemoved
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

// Flush writes every dirty file to disk, unlinks tombstoned paths, and
// clears both marks — the only place the mutation layer touches the
// filesystem.
func (e *Engine) Flush() (written, removed []RelativePath, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, dir := range sortedKeys(e.Packages) {
		unit := e.Packages[dir]
		for _, pkg := range []*Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for _, path := range sortedKeys(pkg.Files) {
				file := pkg.Files[path]
				if !file.IsDirty {
					continue
				}
				abs := e.absPath(path)
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return written, removed, err
				}
				if err := os.WriteFile(abs, file.Src, 0o644); err != nil {
					return written, removed, err
				}
				file.IsDirty = false
				written = append(written, path)
			}
		}
	}
	for _, path := range sortedKeys(e.removed) {
		if err := os.Remove(e.absPath(path)); err != nil && !os.IsNotExist(err) {
			return written, removed, err
		}
		delete(e.removed, path)
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
	for _, unit := range e.Packages {
		for _, pkg := range []*Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for path, file := range pkg.Files {
				if file.IsDirty {
					discarded = append(discarded, path)
				}
			}
		}
	}
	for path := range e.removed {
		discarded = append(discarded, path)
	}
	e.mu.RUnlock()
	slices.Sort(discarded)
	discarded = slices.Compact(discarded)
	if err := e.Bootstrap(ctx); err != nil {
		return nil, err
	}
	return discarded, nil
}

// ----- Creators -----

// CreatePackage creates a new package at dir with one file named after the
// package. name defaults to the directory base. Fails if the directory
// already holds a package; the directory itself is created at Flush.
func (tx *Tx) CreatePackage(dir RelativePath, name string) error {
	dir = dir.Clean()
	if dir.escapesRoot() || dir == "." {
		return fmt.Errorf("cannot create a package at %q", dir)
	}
	if _, ok := tx.eng.Packages[dir]; ok {
		return fmt.Errorf("a package already exists at %q", dir)
	}
	if name == "" {
		name = filepath.Base(string(dir))
	}
	if !token.IsIdentifier(name) {
		return fmt.Errorf("%q is not a valid package name", name)
	}
	pkg := &Package{
		Name:    name,
		Path:    dir,
		Files:   make(map[RelativePath]*File),
		Symbols: make(map[string]*Symbol),
	}
	if err := tx.reloadFile(pkg, dir.Join(name+".go"), []byte("package "+name+"\n")); err != nil {
		return err
	}
	tx.eng.Packages[dir] = &Unit{Prod: pkg}
	return nil
}

// CreateFile adds an empty file to an existing package.
func (tx *Tx) CreateFile(dir RelativePath, name string) error {
	pkg, ok := tx.Package(dir)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", dir)
	}
	path, err := fileAddress(pkg, name)
	if err != nil {
		return err
	}
	if _, _, exists := tx.File(path); exists {
		return fmt.Errorf("file %q already exists", path)
	}
	return tx.reloadFile(pkg, path, []byte("package "+pkg.Name+"\n"))
}

// CreateSymbol adds one new top-level declaration to a file of an existing
// package, at its canonical position. The file is required, never inferred —
// but a missing file inside the package is created implicitly, since
// creation cannot destroy.
func (tx *Tx) CreateSymbol(dir RelativePath, fileName, src string) error {
	pkg, ok := tx.Package(dir)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", dir)
	}
	frag, err := parseDeclFragment(src)
	if err != nil {
		return err
	}
	for _, key := range frag.keys {
		if key == "init" {
			continue // any number of init functions is legal
		}
		if _, exists := pkg.Symbols[key]; exists {
			return fmt.Errorf("symbol %q already exists in %q: use ReplaceSymbol", key, dir)
		}
	}
	path, err := fileAddress(pkg, fileName)
	if err != nil {
		return err
	}
	file, ok := pkg.Files[path]
	if !ok {
		candidate := []byte("package " + pkg.Name + "\n\n" + src + "\n")
		return tx.reloadFile(pkg, path, candidate)
	}
	at := tx.insertOffset(file, frag)
	return tx.reloadFile(pkg, path, applyEdits(file.Src, []edit{{span: span{start: at, end: at}, repl: []byte("\n\n" + src + "\n")}}))
}

// ----- Editors -----

// ReplaceSymbol replaces key's whole declaration with src — for members of
// grouped declarations, src is the member's spec as written inside the
// group. A replacement may rename; the new key must not collide.
func (tx *Tx) ReplaceSymbol(dir RelativePath, key, src string) error {
	sym, owner, ok := tx.Symbol(dir, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, dir)
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
		if _, exists := owner.Symbols[newKey]; exists {
			return fmt.Errorf("replacement declares %q, which already exists in %q", newKey, dir)
		}
	}
	file := owner.Files[sym.File]
	return tx.reloadFile(owner, sym.File, applyEdits(file.Src, []edit{{span: sp, repl: []byte(src)}}))
}

// DeleteSymbol removes key's declaration — its spec alone when it lives in
// a grouped declaration with siblings.
func (tx *Tx) DeleteSymbol(dir RelativePath, key string) error {
	sym, owner, ok := tx.Symbol(dir, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, dir)
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
	file := owner.Files[sym.File]
	return tx.reloadFile(owner, sym.File, applyEdits(file.Src, []edit{{span: sp}}))
}

// DeleteFile removes a file and tombstones its path for Flush.
func (tx *Tx) DeleteFile(path RelativePath) error {
	path = path.Clean()
	_, owner, ok := tx.File(path)
	if !ok {
		return fmt.Errorf("no file at %q", path)
	}
	delete(owner.Files, path)
	owner.RebuildIndex()
	tx.eng.removed[path] = tombstone(owner.Name)
	tx.touch(path)
	tx.pruneEmpty(path.Dir())
	return nil
}

// DeletePackage removes a whole directory's packages, tombstoning every
// file.
func (tx *Tx) DeletePackage(dir RelativePath) error {
	dir = dir.Clean()
	unit, ok := tx.eng.Packages[dir]
	if !ok {
		return fmt.Errorf("no package at %q", dir)
	}
	for _, pkg := range []*Package{unit.Prod, unit.XTest} {
		if pkg == nil {
			continue
		}
		for path := range pkg.Files {
			tx.eng.removed[path] = tombstone(pkg.Name)
			tx.touch(path)
		}
	}
	delete(tx.eng.Packages, dir)
	return nil
}

// ----- Refactorings -----

// MoveSymbol relocates key's declaration to another file of the same
// package: a pure splice, no reference is touched. A member of a grouped
// declaration is extracted as a standalone declaration; extraction refuses
// members whose meaning depends on their position in the group. Moves
// never cross the test build boundary, and the destination file is created
// when missing.
func (tx *Tx) MoveSymbol(dir RelativePath, key, fileName string) error {
	sym, owner, ok := tx.Symbol(dir, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, dir)
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
	file := owner.Files[sym.File]
	src, sp, err := tx.extractDecl(sym, file)
	if err != nil {
		return err
	}
	frag, err := parseDeclFragment(src)
	if err != nil {
		return err
	}
	dest, inOwner := owner.Files[destPath]
	if _, _, exists := tx.File(destPath); exists && !inOwner {
		return fmt.Errorf("file %q belongs to another package", destPath)
	}
	if err := tx.reloadFile(owner, sym.File, applyEdits(file.Src, []edit{{span: sp}})); err != nil {
		return err
	}
	if !inOwner {
		return tx.reloadFile(owner, destPath, []byte("package "+owner.Name+"\n\n"+src+"\n"))
	}
	at := tx.insertOffset(dest, frag)
	return tx.reloadFile(owner, destPath, applyEdits(dest.Src, []edit{{span: span{start: at, end: at}, repl: []byte("\n\n" + src + "\n")}}))
}

// RenameSymbol renames key to newName everywhere: the defining identifier
// and every resolved use across the workspace, matched by qualified name.
// v1 renames exactly this one object — renaming an interface method does
// not chase implementors; broken satisfactions arrive in the echo instead.
func (tx *Tx) RenameSymbol(dir RelativePath, key, newName string) error {
	if !token.IsIdentifier(newName) {
		return fmt.Errorf("%q is not a valid identifier", newName)
	}
	sym, owner, ok := tx.Symbol(dir, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, dir)
	}
	newKey := newName
	if sym.Kind == KindMethod {
		newKey = sym.Recv + "." + newName
	}
	if _, exists := owner.Symbols[newKey]; exists {
		return fmt.Errorf("symbol %q already exists in %q", newKey, dir)
	}
	target := objKey(tx.objectOf(sym))
	if target == "" {
		return fmt.Errorf("type information unavailable for %q", key)
	}

	edits := make(map[RelativePath][]edit)
	def := definingIdent(sym)
	if sp, ok := tx.offsetSpan(sym.File, def.Pos(), def.End()); ok {
		edits[sym.File] = append(edits[sym.File], edit{span: sp, repl: []byte(newName)})
	}
	tx.gatherUses(target, func(relFile RelativePath, sp span) {
		edits[relFile] = append(edits[relFile], edit{span: sp, repl: []byte(newName)})
	})
	return tx.applyFileEdits(edits)
}

// RenameFile moves a file within its package — semantically free in Go,
// files are storage. The old path is tombstoned for Flush.
func (tx *Tx) RenameFile(path RelativePath, newName string) error {
	path = path.Clean()
	file, owner, ok := tx.File(path)
	if !ok {
		return fmt.Errorf("no file at %q", path)
	}
	newPath, err := fileAddress(owner, newName)
	if err != nil {
		return err
	}
	if _, _, exists := tx.File(newPath); exists {
		return fmt.Errorf("file %q already exists", newPath)
	}
	moved := *file
	moved.Path = newPath
	moved.IsDirty = true
	delete(owner.Files, path)
	owner.Files[newPath] = &moved
	tx.eng.removed[path] = tombstone(owner.Name)
	delete(tx.eng.removed, newPath)
	tx.touch(path, newPath)
	owner.RebuildIndex()
	return nil
}

// RenamePackage moves a package directory, rewriting the import path in
// every importer. When the package name equals the old directory base (the
// convention), the package clause and every unaliased qualifier are renamed
// too; aliased imports keep their alias untouched.
func (tx *Tx) RenamePackage(dir, newDir RelativePath) error {
	dir, newDir = dir.Clean(), newDir.Clean()
	if dir == "." || newDir == "." || newDir.escapesRoot() {
		return fmt.Errorf("cannot rename %q to %q", dir, newDir)
	}
	unit, ok := tx.eng.Packages[dir]
	if !ok {
		return fmt.Errorf("no package at %q", dir)
	}
	if _, exists := tx.eng.Packages[newDir]; exists {
		return fmt.Errorf("a package already exists at %q", newDir)
	}
	oldBase, newBase := filepath.Base(string(dir)), filepath.Base(string(newDir))
	renameName := unit.Prod != nil && unit.Prod.Name == oldBase && oldBase != newBase
	if renameName && !token.IsIdentifier(newBase) {
		return fmt.Errorf("%q is not a valid package name", newBase)
	}
	var oldImport, newImport string
	if unit.Prod != nil && strings.HasSuffix(unit.Prod.PkgPath, string(dir)) {
		oldImport = unit.Prod.PkgPath
		newImport = strings.TrimSuffix(oldImport, string(dir)) + string(newDir)
	}

	// Importers first: their files are disjoint from the moving package's.
	// The unit's own XTest package is an importer too — it imports its
	// production sibling — so only Prod itself is skipped.
	edits := make(map[RelativePath][]edit)
	if oldImport != "" {
		for _, pkg := range tx.Packages() {
			if pkg == unit.Prod {
				continue
			}
			for _, file := range tx.Files(pkg) {
				for _, imp := range file.Ast.Imports {
					if imp.Path.Value != strconv.Quote(oldImport) {
						continue
					}
					if sp, ok := tx.offsetSpan(file.Path, imp.Path.Pos(), imp.Path.End()); ok {
						edits[file.Path] = append(edits[file.Path], edit{span: sp, repl: []byte(strconv.Quote(newImport))})
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
					relFile, err := tx.eng.relativePath(tx.eng.FileSet.Position(ident.Pos()).Filename)
					if err != nil || relFile.escapesRoot() {
						continue
					}
					if sp, ok := tx.offsetSpan(relFile, ident.Pos(), ident.End()); ok {
						edits[relFile] = append(edits[relFile], edit{span: sp, repl: []byte(newBase)})
					}
				}
			}
		}
	}
	if err := tx.applyFileEdits(edits); err != nil {
		return err
	}

	// Move the directory's packages, renaming package clauses when due.
	newUnit := &Unit{}
	for i, pkg := range []*Package{unit.Prod, unit.XTest} {
		if pkg == nil {
			continue
		}
		newPkg := pkg.clone()
		newPkg.Path = newDir
		newPkg.Files = make(map[RelativePath]*File, len(pkg.Files))
		if renameName {
			newPkg.Name = newBase + strings.TrimPrefix(pkg.Name, oldBase)
		}
		if newImport != "" && strings.HasSuffix(pkg.PkgPath, string(dir)) {
			newPkg.PkgPath = strings.TrimSuffix(pkg.PkgPath, string(dir)) + string(newDir)
		}
		for path, file := range pkg.Files {
			newPath := newDir.Join(filepath.Base(string(path)))
			tx.eng.removed[path] = tombstone(pkg.Name)
			delete(tx.eng.removed, newPath)
			tx.touch(path, newPath)
			if renameName {
				sp, ok := tx.offsetSpan(path, file.Ast.Name.Pos(), file.Ast.Name.End())
				if !ok {
					return fmt.Errorf("cannot locate package clause of %q", path)
				}
				candidate := applyEdits(file.Src, []edit{{span: sp, repl: []byte(newPkg.Name)}})
				if err := tx.reloadFile(newPkg, newPath, candidate); err != nil {
					return err
				}
				continue
			}
			moved := *file
			moved.Path = newPath
			moved.IsDirty = true
			newPkg.Files[newPath] = &moved
		}
		newPkg.RebuildIndex()
		if i == 0 {
			newUnit.Prod = newPkg
		} else {
			newUnit.XTest = newPkg
		}
	}
	delete(tx.eng.Packages, dir)
	tx.eng.Packages[newDir] = newUnit
	return nil
}

// ----- Pipeline -----

// edit is one splice: replace span with repl (nil deletes).
type edit struct {
	span
	repl []byte
}

// applyEdits splices every edit into src, applying in descending offset
// order so earlier spans stay valid.
func applyEdits(src []byte, edits []edit) []byte {
	slices.SortFunc(edits, func(a, b edit) int { return cmp.Compare(b.start, a.start) })
	out := slices.Clone(src)
	for _, e := range edits {
		out = slices.Concat(out[:e.start], e.repl, out[e.end:])
	}
	return out
}

// reloadFile is the single choke point every content mutation commits
// through: run candidate bytes through goimports and a reparse, install a
// fresh File, rebuild the package index. It never touches disk, and every
// fallible step precedes the swap.
func (tx *Tx) reloadFile(pkg *Package, path RelativePath, candidate []byte) error {
	abs := tx.eng.absPath(path)
	formatted, err := imports.Process(abs, candidate, nil)
	if err != nil {
		return fmt.Errorf("%s does not format: %w", path, err)
	}
	astFile, err := parser.ParseFile(tx.eng.FileSet, abs, formatted, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("%s does not parse: %w", path, err)
	}
	pkg.Files[path] = &File{Path: path, Src: formatted, Ast: astFile, IsDirty: true}
	delete(tx.eng.removed, path)
	tx.touch(path)
	pkg.RebuildIndex()
	return nil
}

// applyFileEdits splices per-file edit batches and reloads each touched
// file, deduplicating overlapping gathers.
func (tx *Tx) applyFileEdits(edits map[RelativePath][]edit) error {
	for _, path := range sortedKeys(edits) {
		file, owner, ok := tx.File(path)
		if !ok {
			return fmt.Errorf("cannot resolve %q while applying edits", path)
		}
		batch := edits[path]
		slices.SortFunc(batch, func(a, b edit) int { return cmp.Compare(a.start, b.start) })
		batch = slices.CompactFunc(batch, func(a, b edit) bool { return a.span == b.span })
		if err := tx.reloadFile(owner, path, applyEdits(file.Src, batch)); err != nil {
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
	candidates := make(map[string]string) // package name -> import path
	ambiguous := make(map[string]bool)
	for _, unit := range tx.eng.Packages {
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
		if !ok || owner.PkgPath == path || importsPath(file.Ast, path) {
			continue
		}
		if needed[diag.File] == nil {
			needed[diag.File] = make(map[string]bool)
		}
		needed[diag.File][path] = true
	}

	repaired := false
	for _, filePath := range sortedKeys(needed) {
		file, owner, ok := tx.File(filePath)
		if !ok {
			continue
		}
		sp, ok := tx.offsetSpan(filePath, file.Ast.Name.Pos(), file.Ast.Name.End())
		if !ok {
			continue
		}
		var repl strings.Builder
		for _, path := range sortedKeys(needed[filePath]) {
			fmt.Fprintf(&repl, "\n\nimport %q", path)
		}
		candidate := applyEdits(file.Src, []edit{{span: span{start: sp.end, end: sp.end}, repl: []byte(repl.String())}})
		if err := tx.reloadFile(owner, filePath, candidate); err != nil {
			continue // repair is best-effort; the diagnostic stays visible
		}
		repaired = true
	}
	return repaired
}

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
			relFile, err := tx.eng.relativePath(tx.eng.FileSet.Position(ident.Pos()).Filename)
			if err != nil || relFile.escapesRoot() {
				continue
			}
			if sp, ok := tx.offsetSpan(relFile, ident.Pos(), ident.End()); ok {
				fn(relFile, sp)
			}
		}
	}
}

// insertOffset returns the canonical insertion offset for a new declaration
// per the placement policy: const/var at the top after imports, types after
// values, funcs at the bottom, methods right after their receiver group. A
// method whose receiver group isn't in this file falls to the bottom.
func (tx *Tx) insertOffset(file *File, frag fragment) int {
	effective := frag
	if frag.kind == KindMethod && !hasReceiverAnchor(file.Ast, frag.recv) {
		effective = fragment{kind: KindFunc}
	}
	var anchor ast.Decl
	for _, decl := range file.Ast.Decls {
		if declPrecedes(decl, effective) {
			anchor = decl
		}
	}
	if anchor == nil {
		// Nothing precedes: insert right after the package clause.
		if sp, ok := tx.offsetSpan(file.Path, file.Ast.Name.Pos(), file.Ast.Name.End()); ok {
			return sp.end
		}
		return len(file.Src)
	}
	if sp, ok := tx.offsetSpan(file.Path, anchor.Pos(), anchor.End()); ok {
		return sp.end
	}
	return len(file.Src)
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
			return d.Recv != nil && recvTypeName(d.Recv) == frag.recv
		}
		return false
	}
	return declRank(decl) <= fragRank(frag.kind)
}

func declRank(decl ast.Decl) int {
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

func fragRank(kind SymbolKind) int {
	switch kind {
	case KindConst, KindVar:
		return 1
	case KindType:
		return 2
	default:
		return 3
	}
}

func hasReceiverAnchor(astFile *ast.File, recv string) bool {
	for _, decl := range astFile.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok == token.TYPE && declaresType(d, recv) {
				return true
			}
		case *ast.FuncDecl:
			if d.Recv != nil && recvTypeName(d.Recv) == recv {
				return true
			}
		}
	}
	return false
}

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
	tmp := &File{Path: "fragment.go", Ast: astFile}
	symbols := make(map[string]*Symbol)
	tmp.index(symbols)
	frag := fragment{keys: sortedKeys(symbols)}
	for _, key := range frag.keys {
		frag.kind = symbols[key].Kind
		frag.recv = symbols[key].Recv
	}
	for range tmp.Inits {
		frag.keys = append(frag.keys, "init")
		frag.kind = KindFunc
	}
	return frag
}

// ----- Internal helpers -----

func cloneUnits(units map[RelativePath]*Unit) map[RelativePath]*Unit {
	out := make(map[RelativePath]*Unit, len(units))
	for dir, unit := range units {
		cloned := &Unit{}
		if unit.Prod != nil {
			cloned.Prod = unit.Prod.clone()
		}
		if unit.XTest != nil {
			cloned.XTest = unit.XTest.clone()
		}
		out[dir] = cloned
	}
	return out
}

// clone copies the package shallowly with fresh maps; File values are
// shared and treated as immutable — mutations install fresh *File instances.
func (p *Package) clone() *Package {
	cloned := *p
	cloned.Files = maps.Clone(p.Files)
	cloned.Symbols = maps.Clone(p.Symbols)
	return &cloned
}

// changedSet is the union of dirty files and tombstoned paths.
func (e *Engine) changedSet() map[RelativePath]bool {
	out := make(map[RelativePath]bool)
	for _, unit := range e.Packages {
		for _, pkg := range []*Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for path, file := range pkg.Files {
				if file.IsDirty {
					out[path] = true
				}
			}
		}
	}
	for path := range e.removed {
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
		if mask, tombstoned := e.removed[path]; tombstoned {
			overlay[e.absPath(path)] = mask
			continue
		}
		if file, _, ok := (&View{eng: e}).File(path); ok {
			overlay[e.absPath(path)] = file.Src
			dirty[path] = true
		}
	}

	fset, units, err := e.load(ctx, overlay)
	if err != nil {
		return err
	}
	for path := range e.removed {
		pruneFileFrom(units, path)
	}
	for path := range dirty {
		if unit, ok := units[path.Dir()]; ok {
			for _, pkg := range []*Package{unit.Prod, unit.XTest} {
				if pkg == nil {
					continue
				}
				if file, ok := pkg.Files[path]; ok {
					file.IsDirty = true
				}
			}
		}
	}
	e.FileSet, e.Packages = fset, units
	return nil
}

// pruneFileFrom removes a tombstoned path from freshly loaded state (the
// overlay can only mask files as empty, not delete them).
func pruneFileFrom(units map[RelativePath]*Unit, path RelativePath) {
	unit, ok := units[path.Dir()]
	if !ok {
		return
	}
	for _, pkg := range []*Package{unit.Prod, unit.XTest} {
		if pkg == nil {
			continue
		}
		if _, ok := pkg.Files[path]; ok {
			delete(pkg.Files, path)
			pkg.RebuildIndex()
		}
	}
	if unit.Prod != nil && len(unit.Prod.Files) == 0 {
		unit.Prod = nil
	}
	if unit.XTest != nil && len(unit.XTest.Files) == 0 {
		unit.XTest = nil
	}
	if unit.Prod == nil && unit.XTest == nil {
		delete(units, path.Dir())
	}
}

func (tx *Tx) pruneEmpty(dir RelativePath) {
	unit, ok := tx.eng.Packages[dir]
	if !ok {
		return
	}
	if unit.Prod != nil && len(unit.Prod.Files) == 0 {
		unit.Prod = nil
	}
	if unit.XTest != nil && len(unit.XTest.Files) == 0 {
		unit.XTest = nil
	}
	if unit.Prod == nil && unit.XTest == nil {
		delete(tx.eng.Packages, dir)
	}
}

// declSpan is the byte span of the whole declaration, doc comment included.
func (v *View) declSpan(sym *Symbol) (span, bool) {
	start := sym.Decl.Pos()
	if doc := docOf(sym.Decl); doc != nil {
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
	if doc := docOf(sym.Spec); doc != nil {
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
		return string(file.Src[sp.start:sp.end]), sp, nil
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
	doc := string(file.Src[sp.start:body.start])
	return doc + gen.Tok.String() + " " + string(file.Src[body.start:body.end]), sp, nil
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
