package workspace

import (
	"errors"
	"strconv"

	"github.com/pedropaccola/gomcp/internal/address"
)

// ErrNarrowlyChecked is returned by SymbolsImplementing when the current
// generation was assembled by a dirty-scoped recheck: some packages were
// carried forward from an earlier type-checking session rather than
// rebuilt in this one, so types.Implements can't be trusted across that
// boundary the way ObjectKey-based matching can (see ObjectKey's own doc
// comment). Callers should force a full recheck and retry — in
// internal/engine, Engine.EnsureFullyChecked does this.
var ErrNarrowlyChecked = errors.New("workspace was narrowly rechecked: SymbolsImplementing needs a full recheck first")

// ComputeRecheckScope computes the set of packages a recheck must re-type-check
// after dirty changed: dirty itself, plus every workspace package that
// (transitively) imports one of them — a change inside a package can
// surface as a new diagnostic anywhere that depends on it, directly or
// through an intermediate importer. Builds the reverse-import graph once
// by scanning every package's own file.Ast().Imports (the same primitive
// PackageMoveSplices already uses for one target import, generalized to
// every package at once) and walks it breadth-first from dirty. External
// imports — anything outside w.UnitKeys() — are dead ends: they can't
// import a workspace package back. Aggregate-owned analysis, since it's
// pure Entity-graph traversal with no engine-specific knowledge.
func (w *Workspace) ComputeRecheckScope(dirty map[address.PkgPath]bool) map[address.PkgPath]bool {
	importedBy := make(map[address.PkgPath][]address.PkgPath) // imported -> importing unit addresses
	for _, addr := range w.UnitKeys() {
		unit, _ := w.Unit(addr)
		for _, pkg := range []*Package{unit.Prod(), unit.XTest()} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				for _, imp := range file.Ast().Imports {
					path, err := strconv.Unquote(imp.Path.Value)
					if err != nil {
						continue
					}
					target := address.PkgPath(path)
					if _, ok := w.Unit(target); !ok {
						continue // external dependency, not a workspace package
					}
					importedBy[target] = append(importedBy[target], addr)
				}
			}
		}
	}

	scope := make(map[address.PkgPath]bool, len(dirty))
	queue := make([]address.PkgPath, 0, len(dirty))
	for pkg := range dirty {
		if !scope[pkg] {
			scope[pkg] = true
			queue = append(queue, pkg)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, importer := range importedBy[cur] {
			if !scope[importer] {
				scope[importer] = true
				queue = append(queue, importer)
			}
		}
	}
	return scope
}

// NarrowlyChecked reports whether the current generation was assembled by
// a dirty-scoped recheck rather than a full one — see SwapLoaded.
func (w *Workspace) NarrowlyChecked() bool {
	return w.narrowlyChecked
}
