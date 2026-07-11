package workspace

import (
	"fmt"
	"go/parser"
	"maps"

	"github.com/pedropaccola/gomcp/internal/address"
)

// Clone copies the mutable model for a transaction: units deeply, the
// tombstone map by value, position tables and the dependency cache shared
// (both are append-only within a transaction's lifetime). Edit works on
// the clone and discards it on error — error means nothing happened.
func (w *Workspace) Clone() *Workspace {
	units := make(map[address.PkgPath]*Unit, len(w.units))
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
func (w *Workspace) SwapFile(pkg *Package, path address.RelativePath, filename string, src []byte) error {
	if pkg.External {
		return fmt.Errorf("dependency %q is read-only", pkg.PkgPath)
	}
	astFile, err := parser.ParseFile(w.fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("%s does not parse: %w", path, err)
	}
	if pkg.files == nil {
		pkg.files = make(map[address.RelativePath]*File)
	}
	pkg.files[path] = &File{Path: path, src: src, ast: astFile, dirty: true}
	delete(w.removed, path)
	pkg.RebuildIndex()
	return nil
}

// DropFile removes one file from its owner: tombstoned for the disk
// boundary, index rebuilt, and the unit pruned once its last file is gone
// — an address with no files is no address.
func (w *Workspace) DropFile(addr address.PkgPath, owner *Package, path address.RelativePath) {
	delete(owner.files, path)
	owner.RebuildIndex()
	w.removed[path] = tombstoneMask(owner.Name)
	w.pruneEmptyUnit(addr)
}

// MoveFile relocates a file within its owner — semantically free in Go,
// files are storage. The old path is tombstoned, the new one untombstoned,
// and the moved copy marked dirty for the next flush.
func (w *Workspace) MoveFile(owner *Package, oldPath, newPath address.RelativePath) {
	file := owner.files[oldPath]
	moved := *file
	moved.Path = newPath
	moved.dirty = true
	delete(owner.files, oldPath)
	owner.files[newPath] = &moved
	w.removed[oldPath] = tombstoneMask(owner.Name)
	delete(w.removed, newPath)
	owner.RebuildIndex()
}

// pruneEmptyUnit drops a unit's packages once their last file is deleted,
// and the unit itself once both are gone.
func (w *Workspace) pruneEmptyUnit(pkg address.PkgPath) {
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
func PruneFile(units map[address.PkgPath]*Unit, pkg address.PkgPath, path address.RelativePath) {
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
