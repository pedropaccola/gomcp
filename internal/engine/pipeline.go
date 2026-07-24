package engine

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
	"golang.org/x/tools/imports"
)

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
func (tx *Tx) reloadFile(pkg *workspace.Package, path address.RelativePath, candidate []byte) error {
	abs := tx.eng.absPath(path)
	formatted, err := imports.Process(abs, candidate, nil)
	if err != nil {
		return fmt.Errorf("%s does not format: %w", path, err)
	}
	if err := tx.ws.SwapFile(pkg, path, abs, formatted); err != nil {
		return err
	}
	tx.touch(path)
	return nil
}

// applyFileSplices applies per-file splice batches and reloads each touched
// file, deduplicating overlapping gathers.
func (tx *Tx) applyFileSplices(splices map[address.RelativePath][]splice) error {
	for _, path := range sortedKeys(splices) {
		file, owner, ok := tx.resolveFile(path)
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
	candidates := make(map[string]address.PkgPath) // package name -> import path
	ambiguous := make(map[string]bool)
	for _, addr := range tx.ws.UnitKeys() {
		unit, _ := tx.ws.Unit(addr)
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

	needed := make(map[address.RelativePath]map[string]bool) // file -> import paths
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
		file, owner, ok := tx.resolveFile(diag.File)
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
		file, owner, ok := tx.resolveFile(filePath)
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

// gatherUses walks every package's resolved uses matching the qualified
// target key and hands each identifier's file and span to fn.
func (tx *Tx) gatherUses(target string, fn func(address.RelativePath, span)) {
	for _, pkg := range tx.allPackages() {
		if pkg.TypesInfo() == nil {
			continue
		}
		for ident, obj := range pkg.TypesInfo().Uses {
			if objKey(obj) != target {
				continue
			}
			relFile, err := tx.eng.relativePath(tx.ws.FileSet().Position(ident.Pos()).Filename)
			if err != nil || relFile.EscapesRoot() {
				continue
			}
			if sp, ok := tx.offsetSpan(relFile, ident.Pos(), ident.End()); ok {
				fn(relFile, sp)
			}
		}
	}
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

// fileAddress validates a bare *.go name inside pkg's directory.
func fileAddress(pkg *workspace.Package, name string) (address.RelativePath, error) {
	if name == "" || name != filepath.Base(name) || !strings.HasSuffix(name, ".go") {
		return "", fmt.Errorf("file name must be a bare *.go name, got %q", name)
	}
	return pkg.Path.Join(name), nil
}

// renderDocComment formats plain text as a leading Go doc comment, one
// line comment per line, blank lines kept bare (no trailing space) per
// gofmt's own convention. Empty input renders to nothing.
func renderDocComment(doc string) []byte {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return nil
	}
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		if line == "" {
			b.WriteString("//\n")
		} else {
			b.WriteString("// " + line + "\n")
		}
	}
	return []byte(b.String())
}
