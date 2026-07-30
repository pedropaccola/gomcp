package store

import (
	"fmt"
	"go/token"
	"strings"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// applyFileSplices applies splices across however many files they touch
// and records every one as changed — the one door Tx's mutation verbs
// use to turn a splice plan into installed content.
func (tx *Tx) applyFileSplices(splices []workspace.Splice) error {
	touched, err := tx.ws.ApplyFileSplices(splices)
	if err != nil {
		return err
	}
	tx.markChanged(touched...)
	return nil
}

// installFile is the one door through which file content enters the
// model on the mutation path: hand candidate bytes to the workspace's
// formatting, parse-enforcing SwapFile, then record the touch. addr/
// isXTest select the package to swap into, resolved fresh by SwapFile
// itself rather than trusted from a pointer the caller might have
// resolved before an intervening mutation. Every fallible step precedes
// the swap; an error means state is untouched.
func (tx *Tx) installFile(addr workspace.PackagePath, isXTest bool, newPath workspace.FilePath, candidate []byte) error {
	if err := tx.ws.SwapFile(addr, isXTest, newPath, candidate); err != nil {
		return err
	}
	tx.markChanged(newPath)
	return nil
}

// markChanged records paths as changed by this transaction; every verb reports
// its footprint here regardless of prior dirtiness.
func (tx *Tx) markChanged(paths ...workspace.FilePath) {
	for _, path := range paths {
		tx.changed[path] = true
	}
}

// RepairMissingImports is the bounded self-repair pass behind Store.Edit:
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
	candidates := make(map[string]workspace.PackagePath) // package name -> import path
	ambiguous := make(map[string]bool)
	for _, addr := range tx.ws.UnitKeys() {
		unit, _ := tx.ws.Unit(addr)
		pkg := unit.Prod()
		if pkg == nil || pkg.ID.Base() == "" || pkg.Name == "main" {
			continue
		}
		if _, dup := candidates[pkg.Name]; dup {
			ambiguous[pkg.Name] = true
			delete(candidates, pkg.Name)
			continue
		}
		candidates[pkg.Name] = pkg.ID.Base()
	}

	needed := make(map[workspace.FilePath]map[string]bool) // file -> import paths
	for _, diag := range tx.AllDiagnostics() {
		if diag.Kind != workspace.DiagType || diag.File == "" {
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
		file, owner, ok := tx.ws.ResolveFileByPath(diag.File)
		if !ok || owner.ID.Base() == path || importsPath(file.Ast(), string(path)) {
			continue
		}
		if needed[diag.File] == nil {
			needed[diag.File] = make(map[string]bool)
		}
		needed[diag.File][string(path)] = true
	}

	repaired := false
	for _, filePath := range sortedKeys(needed) {
		file, owner, ok := tx.ws.ResolveFileByPath(filePath)
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
		addr := filePath.PackagePath()
		if err := tx.installFile(addr, owner.ID.Kind() == workspace.KindXTest, filePath, candidate); err != nil {
			continue // repair is best-effort; the diagnostic stays visible
		}
		repaired = true
	}
	return repaired
}
