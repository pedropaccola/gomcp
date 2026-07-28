package gate

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
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// applyFileSplices groups splices by file and installs each file's result,
// deduplicating overlapping gathers.
func (tx *Tx) applyFileSplices(splices []workspace.Splice) error {
	byPath := make(map[address.FilePath][]workspace.Splice)
	for _, s := range splices {
		byPath[s.Path] = append(byPath[s.Path], s)
	}
	for _, path := range sortedKeys(byPath) {
		file, owner, ok := tx.resolveFileByPath(path)
		if !ok {
			return fmt.Errorf("cannot resolve %q while applying splices", path)
		}
		batch := byPath[path]
		slices.SortFunc(batch, func(a, b workspace.Splice) int { return cmp.Compare(a.Start, b.Start) })
		batch = slices.CompactFunc(batch, func(a, b workspace.Splice) bool { return a.Start == b.Start && a.End == b.End })
		addr := address.PkgPath(filepath.Dir(string(path)))
		if err := tx.installFile(addr, tx.isXTestOwner(addr, owner), path, workspace.ApplySplices(file.Src(), batch)); err != nil {
			return err
		}
	}
	return nil
}

// installFile is the one door through which file content enters the
// model on the mutation path: hand candidate bytes to the workspace's
// formatting, parse-enforcing SwapFile, then record the touch. addr/
// isXTest select the package to swap into, resolved fresh by SwapFile
// itself rather than trusted from a pointer the caller might have
// resolved before an intervening mutation. Every fallible step precedes
// the swap; an error means state is untouched.
func (tx *Tx) installFile(addr address.PkgPath, isXTest bool, newPath address.FilePath, candidate []byte) error {
	if err := tx.ws.SwapFile(addr, isXTest, newPath, candidate); err != nil {
		return err
	}
	tx.touch(newPath)
	return nil
}

// RepairMissingImports is the bounded self-repair pass behind Engine.Edit:
// when a recheck reports "undefined: X" and X names exactly one
// in-memory package, the missing import is spliced in. goimports cannot
// discover packages that exist only in memory (it scans disk), and
// imports are not agent-addressable, so the server must cover its own
// blind spot. Best-effort by design: ambiguous names and failed splices
// leave the diagnostic standing, and a wrong repair (an ident that merely
// collides with a package name) surfaces as an ordinary diagnostic on the
// next echo while goimports drops the then-unused import on the file's
// next reload.
func (tx *Tx) RepairMissingImports() bool {
	// Unique importable package names known to the workspace.
	candidates := make(map[string]address.PkgPath) // package name -> import path
	ambiguous := make(map[string]bool)
	for _, addr := range tx.ws.UnitKeys() {
		unit, _ := tx.ws.Unit(addr)
		pkg := unit.Prod()
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

	needed := make(map[address.FilePath]map[string]bool) // file -> import paths
	for _, diag := range tx.AllDiagnostics() {
		if diag.Kind != dto.DiagType || diag.File == "" {
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
		file, owner, ok := tx.resolveFileByPath(diag.File)
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
		file, owner, ok := tx.resolveFileByPath(filePath)
		if !ok {
			continue
		}
		var repl strings.Builder
		for _, path := range sortedKeys(needed[filePath]) {
			fmt.Fprintf(&repl, "\n\nimport %q", path)
		}
		insertAt := file.Ast().Name.End()
		sp, ok := tx.ws.NewSpliceAtPos(owner, filePath, insertAt, insertAt, []byte(repl.String()))
		if !ok {
			continue
		}
		candidate := workspace.ApplySplices(file.Src(), []workspace.Splice{sp})
		addr := address.PkgPath(filepath.Dir(string(filePath)))
		if err := tx.installFile(addr, tx.isXTestOwner(addr, owner), filePath, candidate); err != nil {
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

// isXTestOwner reports whether owner is pkg's external test package
// rather than its production one — the Prod/XTest selector every
// address-based workspace primitive needs alongside an address, for
// callers (like relocateSymbol) that resolved owner through a path that
// doesn't already know which half matched.
func (v *View) isXTestOwner(pkg address.PkgPath, owner *workspace.Package) bool {
	unit, ok := v.ws.Unit(pkg)
	return ok && owner == unit.XTest()
}
