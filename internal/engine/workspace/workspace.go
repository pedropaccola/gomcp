package workspace

import (
	"go/token"
	"maps"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
)

// Workspace is the one mutable root of the model: the unit map, tombstone
// masks, position tables, and the dependency cache live behind it, and
// every structural change flows through its primitives. The fields are
// unexported by construction — the compiler, not convention, keeps
// arbitrary code from reshaping the model.
//
// Not safe for concurrent use: every method assumes exclusive access,
// synchronized externally — internal/engine.Engine's own mutex is the
// only caller that does this today.
type Workspace struct {
	module address.PkgPath
	fset   *token.FileSet
	units  map[address.PkgPath]*Unit
	diags  []Diagnostic // workspace-scoped: module/driver-level problems

	// removed maps tombstoned paths (deleted or renamed away in-memory) to
	// the overlay mask that hides their on-disk content from rechecks;
	// Flush unlinks them. go/packages overlays cannot remove files, only
	// replace their content, hence the mask.
	removed map[address.RelativePath][]byte

	// The read-only dependency cache. External positions live in their own
	// FileSet — workspace swaps must not invalidate cached positions.
	// Negative results are cached too, so a mistyped address costs one
	// load per session.
	external     map[address.PkgPath]*Package
	externalErr  map[address.PkgPath]error
	externalFset *token.FileSet
}

// ----- Lifecycle -----

// NewWorkspace creates an empty model, ready for a Reset from the first
// load.
func NewWorkspace() *Workspace {
	return &Workspace{
		fset:         token.NewFileSet(),
		units:        make(map[address.PkgPath]*Unit),
		removed:      make(map[address.RelativePath][]byte),
		external:     make(map[address.PkgPath]*Package),
		externalErr:  make(map[address.PkgPath]error),
		externalFset: token.NewFileSet(),
	}
}

// Reset replaces the whole model with a fresh load's truth, discarding
// tombstones, workspace diagnostics, and the dependency cache — the
// bootstrap swap. units come from the loader and are trusted as its
// output.
func (w *Workspace) Reset(module address.PkgPath, fset *token.FileSet, units map[address.PkgPath]*Unit) {
	w.module = module
	w.fset = fset
	w.units = units
	w.diags = nil
	w.removed = make(map[address.RelativePath][]byte)
	w.external = make(map[address.PkgPath]*Package)
	w.externalErr = make(map[address.PkgPath]error)
	w.externalFset = token.NewFileSet()
}

// SwapLoaded replaces the units and their position table with a recheck's
// output, keeping tombstones, module identity, and the dependency cache —
// the post-mutation swap.
func (w *Workspace) SwapLoaded(fset *token.FileSet, units map[address.PkgPath]*Unit) {
	w.fset = fset
	w.units = units
}

// Module is the workspace's module path: the prefix of every workspace
// package address.
func (w *Workspace) Module() address.PkgPath {
	return w.module
}

// FileSet is the position table of the workspace's own packages; see
// FsetOf for the owner-aware choice.
func (w *Workspace) FileSet() *token.FileSet {
	return w.fset
}

// FsetOf is the FileSet a package's positions live in: the external
// cache's for dependencies, the workspace FileSet otherwise.
func (w *Workspace) FsetOf(pkg *Package) *token.FileSet {
	if pkg != nil && pkg.External {
		return w.externalFset
	}
	return w.fset
}

// Unit resolves a canonical package address.
func (w *Workspace) Unit(pkg address.PkgPath) (*Unit, bool) {
	unit, ok := w.units[pkg]
	return unit, ok
}

// UnitKeys enumerates every unit's address, sorted — determinism by
// construction: the raw map never leaves the workspace.
func (w *Workspace) UnitKeys() []address.PkgPath {
	return slices.Sorted(maps.Keys(w.units))
}

// InstallUnit maps a unit at an address; creation and package moves end
// here.
func (w *Workspace) InstallUnit(pkg address.PkgPath, unit *Unit) {
	w.units[pkg] = unit
}

// RemoveUnit unmaps an address without tombstoning its files — package
// moves relocate files individually first. DeletePackage-style removal
// tombstones each file, then removes the unit.
func (w *Workspace) RemoveUnit(pkg address.PkgPath) {
	delete(w.units, pkg)
}

// WorkspaceDiags enumerates the workspace-scoped diagnostics:
// module/driver-level problems not attributable to any package.
func (w *Workspace) WorkspaceDiags() []Diagnostic {
	return slices.Clone(w.diags)
}

// ExternalPackage resolves a dependency resident in the cache.
func (w *Workspace) ExternalPackage(pkg address.PkgPath) (*Package, bool) {
	p, ok := w.external[pkg]
	return p, ok
}

// InstallExternal caches a loaded dependency.
func (w *Workspace) InstallExternal(pkg address.PkgPath, p *Package) {
	w.external[pkg] = p
}

// ExternalFailure reports a dependency address's cached load error.
func (w *Workspace) ExternalFailure(pkg address.PkgPath) (error, bool) {
	err, ok := w.externalErr[pkg]
	return err, ok
}

// FailExternal caches a dependency load failure, so a mistyped address
// costs one load per session.
func (w *Workspace) FailExternal(pkg address.PkgPath, err error) {
	w.externalErr[pkg] = err
}

// ExternalFset is the dependency cache's own position table: workspace
// swaps never invalidate cached positions.
func (w *Workspace) ExternalFset() *token.FileSet {
	return w.externalFset
}
