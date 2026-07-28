package workspace

import (
	"fmt"
	"go/parser"
	"maps"

	"github.com/pedropaccola/gomcp/internal/address"
	"golang.org/x/tools/imports"
)

// Clone copies the mutable model for a transaction: units, packages, and
// the tombstone map start shared with the original and fork lazily, only
// for what this transaction actually touches (ensureUnitsForked,
// ensureRemovedForked, ensurePackageForked) — position tables and the
// dependency cache are shared outright (both are append-only within a
// transaction's lifetime). Edit works on the clone and discards it on
// error — error means nothing happened.
func (w *Workspace) Clone() *Workspace {
	cloned := *w
	cloned.unitsForked = false
	cloned.removedForked = false
	cloned.forkedPkgs = nil
	return &cloned
}

// SwapFile is the one way file content enters the model on the mutation
// path: goimports-format the candidate bytes against newPath's own
// address (no real filesystem is consulted — goimports classifies
// imports purely from the filename's shape), parse the formatted
// result, install a fresh dirty File, clear any tombstone at newPath,
// and rebuild the owner's index. Formatting happens here, not at the
// caller — nothing outside workspace needs to know goimports exists.
// Every fallible step precedes the swap — an error means the model is
// untouched.
func (w *Workspace) SwapFile(addr address.PkgPath, isXTest bool, newPath address.FilePath, src []byte) error {
	formatted, err := imports.Process(newPath.String(), src, nil)
	if err != nil {
		return fmt.Errorf("%s does not format: %w", newPath, err)
	}
	astFile, err := parser.ParseFile(w.fset, newPath.String(), formatted, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("%s does not parse: %w", newPath, err)
	}
	pkg := w.ensurePackageForked(addr, isXTest)
	if pkg.files == nil {
		pkg.files = make(map[address.FilePath]*File)
	}
	pkg.files[newPath] = newFile(newPath, formatted, astFile, true)
	w.ensureRemovedForked()
	delete(w.removed, newPath)
	pkg.RebuildIndex()
	return nil
}

// DropFile removes one file from its owner: tombstoned for the disk
// boundary, index rebuilt, and the unit pruned once its last file is gone
// — an address with no files is no address.
func (w *Workspace) DropFile(addr address.PkgPath, isXTest bool, path address.FilePath) {
	owner := w.ensurePackageForked(addr, isXTest)
	delete(owner.files, path)
	owner.RebuildIndex()
	w.ensureRemovedForked()
	w.removed[path] = tombstoneEntry{pkg: addr, mask: tombstoneMask(owner.Name)}
	w.pruneEmptyUnit(addr)
}

// MoveFile relocates a file within its owner — semantically free in Go,
// files are storage. The old path is tombstoned, the new one untombstoned,
// and the moved copy marked dirty for the next flush.
func (w *Workspace) MoveFile(addr address.PkgPath, isXTest bool, oldPath, newPath address.FilePath) {
	owner := w.ensurePackageForked(addr, isXTest)
	file := owner.files[oldPath]
	moved := *file
	moved.Path = newPath
	moved.dirty = true
	delete(owner.files, oldPath)
	owner.files[newPath] = &moved
	w.ensureRemovedForked()
	w.removed[oldPath] = tombstoneEntry{pkg: addr, mask: tombstoneMask(owner.Name)}
	delete(w.removed, newPath)
	owner.RebuildIndex()
}

// pruneEmptyUnit drops a unit's packages once their last file is deleted,
// and the unit itself once both are gone.
func (w *Workspace) pruneEmptyUnit(pkg address.PkgPath) {
	if unit, ok := w.units[pkg]; ok {
		pruneIfEmpty(w.units, pkg, unit)
	}
}

// ForkExternal returns a shallow copy of w with fresh, independent
// external and externalErr maps seeded from the current ones — safe for
// LoadExternal to mutate without racing a reader still holding an older
// generation that shares this Workspace's dependency cache. Everything
// else (units, removed, fset, module) stays shared, since LoadExternal
// never touches them.
func (w *Workspace) ForkExternal() *Workspace {
	forked := *w
	forked.external = maps.Clone(w.external)
	forked.externalErr = maps.Clone(w.externalErr)
	return &forked
}

// ensureUnitsForked forks the unit map (one maps.Clone) the first time
// this generation installs or removes a unit; idempotent after that.
// Forking the map is separate from forking one package's own contents
// (ensurePackageForked) — most mutations only need the latter.
func (w *Workspace) ensureUnitsForked() {
	if w.unitsForked {
		return
	}
	w.units = maps.Clone(w.units)
	w.unitsForked = true
}

// ensureRemovedForked forks the tombstone map the first time this
// generation tombstones or clears a path; idempotent after that.
func (w *Workspace) ensureRemovedForked() {
	if w.removedForked {
		return
	}
	w.removed = maps.Clone(w.removed)
	w.removedForked = true
}

// ensurePackageForked returns the Prod or XTest package at addr, forking
// it — and the Unit wrapper around it — the first time this generation
// mutates it; every other package's pointer stays shared with whatever
// generation this one was cloned from. addr must already be installed —
// CreatePackage and MovePackage install an empty Unit before calling
// this, precisely so it always is.
func (w *Workspace) ensurePackageForked(addr address.PkgPath, isXTest bool) *Package {
	w.ensureUnitsForked()
	unit := w.units[addr]
	pkg := unit.prod
	if isXTest {
		pkg = unit.xtest
	}
	if w.forkedPkgs[pkg] {
		return pkg
	}
	forked := pkg.Clone()
	next := *unit
	if isXTest {
		next.xtest = forked
	} else {
		next.prod = forked
	}
	w.units[addr] = &next
	if w.forkedPkgs == nil {
		w.forkedPkgs = make(map[*Package]bool)
	}
	w.forkedPkgs[forked] = true
	return forked
}

// MarkFlushed clears path's dirty mark on the package at addr — Flush's
// half of the dirty lifecycle; SwapFile and MoveFile set the mark. Forks
// the package first if this generation hasn't already, same as every
// other mutating primitive.
func (w *Workspace) MarkFlushed(addr address.PkgPath, isXTest bool, path address.FilePath) {
	w.ensurePackageForked(addr, isXTest).MarkFlushed(path)
}

// DropTombstonedFile removes path from a freshly loaded unit map — the load-path
// counterpart of DropFile: overlays can only mask a deleted file as empty,
// so the mask's residue must not survive as a real file. Emptied packages
// and units are pruned the way pruneEmptyUnit prunes installed ones.
func DropTombstonedFile(units map[address.PkgPath]*Unit, pkg address.PkgPath, path address.FilePath) {
	unit, ok := units[pkg]
	if !ok {
		return
	}
	for _, p := range []*Package{unit.prod, unit.xtest} {
		if p == nil {
			continue
		}
		if _, ok := p.files[path]; ok {
			delete(p.files, path)
			p.RebuildIndex()
		}
	}
	pruneIfEmpty(units, pkg, unit)
}

// pruneIfEmpty drops unit's Prod/XTest once each is out of files, and
// removes it from units entirely once both are gone. Shared by
// pruneEmptyUnit (an installed workspace) and DropTombstonedFile (a freshly
// loaded unit map, before installation).
func pruneIfEmpty(units map[address.PkgPath]*Unit, pkg address.PkgPath, unit *Unit) {
	if unit.prod != nil && len(unit.prod.files) == 0 {
		unit.prod = nil
	}
	if unit.xtest != nil && len(unit.xtest.files) == 0 {
		unit.xtest = nil
	}
	if unit.prod == nil && unit.xtest == nil {
		delete(units, pkg)
	}
}
