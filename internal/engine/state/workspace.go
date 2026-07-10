package state

import (
	"fmt"
	"go/parser"
	"go/token"
	"maps"
	"slices"
)

// Workspace is the one mutable root of the model: the unit map, tombstone
// masks, position tables, and the dependency cache live behind it, and
// every structural change flows through its primitives. The fields are
// unexported by construction — the compiler, not convention, keeps
// arbitrary code from reshaping the model.
type Workspace struct {
	module PkgPath
	fset   *token.FileSet
	units  map[PkgPath]*Unit
	diags  []Diagnostic // workspace-scoped: module/driver-level problems

	// removed maps tombstoned paths (deleted or renamed away in-memory) to
	// the overlay mask that hides their on-disk content from rechecks;
	// Flush unlinks them. go/packages overlays cannot remove files, only
	// replace their content, hence the mask.
	removed map[RelativePath][]byte

	// The read-only dependency cache. External positions live in their own
	// FileSet — workspace swaps must not invalidate cached positions.
	// Negative results are cached too, so a mistyped address costs one
	// load per session.
	external     map[PkgPath]*Package
	externalErr  map[PkgPath]error
	externalFset *token.FileSet
}

// ----- Lifecycle -----

// NewWorkspace creates an empty model, ready for a Reset from the first
// load.
func NewWorkspace() *Workspace {
	return &Workspace{
		fset:         token.NewFileSet(),
		units:        make(map[PkgPath]*Unit),
		removed:      make(map[RelativePath][]byte),
		external:     make(map[PkgPath]*Package),
		externalErr:  make(map[PkgPath]error),
		externalFset: token.NewFileSet(),
	}
}

// Reset replaces the whole model with a fresh load's truth, discarding
// tombstones, workspace diagnostics, and the dependency cache — the
// bootstrap swap. units come from the loader and are trusted as its
// output.
func (w *Workspace) Reset(module PkgPath, fset *token.FileSet, units map[PkgPath]*Unit) {
	w.module = module
	w.fset = fset
	w.units = units
	w.diags = nil
	w.removed = make(map[RelativePath][]byte)
	w.external = make(map[PkgPath]*Package)
	w.externalErr = make(map[PkgPath]error)
	w.externalFset = token.NewFileSet()
}

// SwapLoaded replaces the units and their position table with a recheck's
// output, keeping tombstones, module identity, and the dependency cache —
// the post-mutation swap.
func (w *Workspace) SwapLoaded(fset *token.FileSet, units map[PkgPath]*Unit) {
	w.fset = fset
	w.units = units
}

// ----- Accessors -----

// Module is the workspace's module path: the prefix of every workspace
// package address.
func (w *Workspace) Module() PkgPath {
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
func (w *Workspace) Unit(pkg PkgPath) (*Unit, bool) {
	unit, ok := w.units[pkg]
	return unit, ok
}

// UnitKeys enumerates every unit's address, sorted — determinism by
// construction: the raw map never leaves the workspace.
func (w *Workspace) UnitKeys() []PkgPath {
	return slices.Sorted(maps.Keys(w.units))
}

// InstallUnit maps a unit at an address; creation and package moves end
// here.
func (w *Workspace) InstallUnit(pkg PkgPath, unit *Unit) {
	w.units[pkg] = unit
}

// RemoveUnit unmaps an address without tombstoning its files — package
// moves relocate files individually first. DeletePackage-style removal
// tombstones each file, then removes the unit.
func (w *Workspace) RemoveUnit(pkg PkgPath) {
	delete(w.units, pkg)
}

// ----- Primitives -----

// Clone copies the mutable model for a transaction: units deeply, the
// tombstone map by value, position tables and the dependency cache shared
// (both are append-only within a transaction's lifetime). Edit works on
// the clone and discards it on error — error means nothing happened.
func (w *Workspace) Clone() *Workspace {
	units := make(map[PkgPath]*Unit, len(w.units))
	for pkg, unit := range w.units {
		cloned := &Unit{}
		if unit.Prod != nil {
			cloned.Prod = unit.Prod.Clone()
		}
		if unit.XTest != nil {
			cloned.XTest = unit.XTest.Clone()
		}
		units[pkg] = cloned
	}
	return &Workspace{
		module:       w.module,
		fset:         w.fset,
		units:        units,
		removed:      maps.Clone(w.removed),
		external:     w.external,
		externalErr:  w.externalErr,
		externalFset: w.externalFset,
	}
}

// SwapFile is the one way file content enters the model on the mutation
// path: parse the candidate bytes (they must already be formatted), install
// a fresh dirty File, clear any tombstone at the path, and rebuild the
// owner's index. Every fallible step precedes the swap — an error means the
// model is untouched. Dependencies are read-only and refuse.
func (w *Workspace) SwapFile(pkg *Package, path RelativePath, filename string, src []byte) error {
	if pkg.External {
		return fmt.Errorf("dependency %q is read-only", pkg.PkgPath)
	}
	astFile, err := parser.ParseFile(w.fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("%s does not parse: %w", path, err)
	}
	if pkg.files == nil {
		pkg.files = make(map[RelativePath]*File)
	}
	pkg.files[path] = &File{Path: path, src: src, ast: astFile, dirty: true}
	delete(w.removed, path)
	pkg.RebuildIndex()
	return nil
}

// DropFile removes one file from its owner: tombstoned for the disk
// boundary, index rebuilt, and the unit pruned once its last file is gone
// — an address with no files is no address.
func (w *Workspace) DropFile(addr PkgPath, owner *Package, path RelativePath) {
	delete(owner.files, path)
	owner.RebuildIndex()
	w.removed[path] = tombstone(owner.Name)
	w.pruneEmptyUnit(addr)
}

// MoveFile relocates a file within its owner — semantically free in Go,
// files are storage. The old path is tombstoned, the new one untombstoned,
// and the moved copy marked dirty for the next flush.
func (w *Workspace) MoveFile(owner *Package, oldPath, newPath RelativePath) {
	file := owner.files[oldPath]
	moved := *file
	moved.Path = newPath
	moved.dirty = true
	delete(owner.files, oldPath)
	owner.files[newPath] = &moved
	w.removed[oldPath] = tombstone(owner.Name)
	delete(w.removed, newPath)
	owner.RebuildIndex()
}

// pruneEmptyUnit drops a unit's packages once their last file is deleted,
// and the unit itself once both are gone.
func (w *Workspace) pruneEmptyUnit(pkg PkgPath) {
	unit, ok := w.units[pkg]
	if !ok {
		return
	}
	if unit.Prod != nil && len(unit.Prod.files) == 0 {
		unit.Prod = nil
	}
	if unit.XTest != nil && len(unit.XTest.files) == 0 {
		unit.XTest = nil
	}
	if unit.Prod == nil && unit.XTest == nil {
		delete(w.units, pkg)
	}
}

// PruneFile removes path from a freshly loaded unit map — the load-path
// counterpart of DropFile: overlays can only mask a deleted file as empty,
// so the mask's residue must not survive as a real file. Emptied packages
// and units are pruned the way pruneEmptyUnit prunes installed ones.
func PruneFile(units map[PkgPath]*Unit, pkg PkgPath, path RelativePath) {
	unit, ok := units[pkg]
	if !ok {
		return
	}
	for _, p := range []*Package{unit.Prod, unit.XTest} {
		if p == nil {
			continue
		}
		if _, ok := p.files[path]; ok {
			delete(p.files, path)
			p.RebuildIndex()
		}
	}
	if unit.Prod != nil && len(unit.Prod.files) == 0 {
		unit.Prod = nil
	}
	if unit.XTest != nil && len(unit.XTest.files) == 0 {
		unit.XTest = nil
	}
	if unit.Prod == nil && unit.XTest == nil {
		delete(units, pkg)
	}
}

// ----- Tombstones -----

// tombstone is the overlay mask for a removed file: syntactically valid,
// semantically empty, so rechecks see the deletion's blast radius.
func tombstone(pkgName string) []byte {
	return []byte("package " + pkgName + "\n")
}

// Tombstone masks a path as removed for the next recheck and flush.
func (w *Workspace) Tombstone(path RelativePath, pkgName string) {
	w.removed[path] = tombstone(pkgName)
}

// ClearTombstone lifts a pending removal — a path recreated or moved onto
// is alive again.
func (w *Workspace) ClearTombstone(path RelativePath) {
	delete(w.removed, path)
}

// Tombstoned reports a path's overlay mask when it is pending removal.
func (w *Workspace) Tombstoned(path RelativePath) ([]byte, bool) {
	mask, ok := w.removed[path]
	return mask, ok
}

// Tombstones enumerates every path pending removal, sorted.
func (w *Workspace) Tombstones() []RelativePath {
	return slices.Sorted(maps.Keys(w.removed))
}

// ----- Dependency cache -----

// ExternalPackage resolves a dependency resident in the cache.
func (w *Workspace) ExternalPackage(pkg PkgPath) (*Package, bool) {
	p, ok := w.external[pkg]
	return p, ok
}

// InstallExternal caches a loaded dependency.
func (w *Workspace) InstallExternal(pkg PkgPath, p *Package) {
	w.external[pkg] = p
}

// ExternalFailure reports a dependency address's cached load error.
func (w *Workspace) ExternalFailure(pkg PkgPath) (error, bool) {
	err, ok := w.externalErr[pkg]
	return err, ok
}

// FailExternal caches a dependency load failure, so a mistyped address
// costs one load per session.
func (w *Workspace) FailExternal(pkg PkgPath, err error) {
	w.externalErr[pkg] = err
}

// ExternalFset is the dependency cache's own position table: workspace
// swaps never invalidate cached positions.
func (w *Workspace) ExternalFset() *token.FileSet {
	return w.externalFset
}

// WorkspaceDiags enumerates the workspace-scoped diagnostics:
// module/driver-level problems not attributable to any package.
func (w *Workspace) WorkspaceDiags() []Diagnostic {
	return slices.Clone(w.diags)
}
