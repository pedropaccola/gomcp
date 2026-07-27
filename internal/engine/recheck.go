package engine

import (
	"context"
	"go/token"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// recheckNarrowLocked reloads the workspace with the in-memory truth
// overlaid on disk, narrowed to dirty packages ∨ their transitive
// importers — see recheckScopedLocked for the mechanism. ws is the
// caller's own candidate — recheckNarrowLocked never touches e.ws — and
// the caller still must hold e.mu, since ws is being built for eventual
// publication, not yet safe to read concurrently.
func (e *Engine) recheckNarrowLocked(ctx context.Context, ws *workspace.Workspace) error {
	return e.recheckScopedLocked(ctx, ws, false)
}

// recheckFullLocked is recheckNarrowLocked's full-module variant: every
// package is rebuilt fresh, nothing carried forward — the same shape a
// dirty-scoped recheck degenerates to when its scope happens to cover
// every package. Used by EnsureFullyChecked to restore a unified
// type-checking universe.
func (e *Engine) recheckFullLocked(ctx context.Context, ws *workspace.Workspace) error {
	return e.recheckScopedLocked(ctx, ws, true)
}

// recheckScopedLocked is the shared body behind recheckNarrowLocked and
// recheckFullLocked. forceFull=false narrows the recheck to dirty
// packages ∨ their transitive importers (Workspace.RecheckScope);
// packages outside that scope are carried forward unchanged, their
// existing *token.File entries folded into the new generation's FileSet
// via AddExistingFiles so every package still shares one consistent
// position table. Their old entries are explicitly removed from the
// outgoing FileSet — not for memory (GC already reclaims an unreferenced
// generation once nothing holds it), but so a bug in the scope
// computation shows up as an immediate FileSet.Position failure instead
// of silently serving a stale position. forceFull=true skips the
// narrowing entirely: every package is rebuilt, nothing kept, restoring a
// single unified type-checking session.
func (e *Engine) recheckScopedLocked(ctx context.Context, ws *workspace.Workspace, forceFull bool) error {
	overlay := make(map[string][]byte)
	dirty := make(map[address.RelativePath]bool)
	dirtyPkgs := make(map[address.PkgPath]bool)
	for path := range changedSet(ws) {
		pkg := pkgAt(ws, path.Dir())
		dirtyPkgs[pkg] = true
		if mask, tombstoned := ws.TombstoneMask(path); tombstoned {
			overlay[e.absPath(path)] = mask
			continue
		}
		if unit, ok := ws.Unit(pkg); ok {
			for _, p := range []*workspace.Package{unit.Prod(), unit.XTest()} {
				if p == nil {
					continue
				}
				if file, ok := p.File(path); ok {
					overlay[e.absPath(path)] = file.Src()
					dirty[path] = true
				}
			}
		}
	}

	scope := ws.ComputeRecheckScope(dirtyPkgs)
	if forceFull {
		scope = make(map[address.PkgPath]bool, len(ws.UnitKeys()))
		for _, addr := range ws.UnitKeys() {
			scope[addr] = true
		}
	}
	patterns := make([]string, 0, len(scope))
	for pkg := range scope {
		patterns = append(patterns, string(pkg))
	}
	if len(patterns) == 0 {
		return nil // nothing dirty and no full recheck requested: nothing to do
	}

	newFset := token.NewFileSet()
	oldFset := ws.FileSet()
	kept := make(map[address.PkgPath]*workspace.Unit)
	for _, addr := range ws.UnitKeys() {
		unit, _ := ws.Unit(addr)
		if scope[addr] {
			for _, pkg := range []*workspace.Package{unit.Prod(), unit.XTest()} {
				if pkg == nil {
					continue
				}
				for _, file := range pkg.Files() {
					if tf := oldFset.File(file.Ast().Pos()); tf != nil {
						oldFset.RemoveFile(tf)
					}
				}
			}
			continue
		}
		kept[addr] = unit
		for _, pkg := range []*workspace.Package{unit.Prod(), unit.XTest()} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if tf := oldFset.File(file.Ast().Pos()); tf != nil {
					newFset.AddExistingFiles(tf)
				}
			}
		}
	}
	// Captured before the merge below: units := kept aliases the same map
	// (Go maps are reference types), so len(kept) after merging freshUnits
	// in would reflect the merged total, not what was actually carried
	// forward.
	narrow := len(kept) > 0

	_, _, freshUnits, err := e.loadInto(ctx, newFset, overlay, patterns...)
	if err != nil {
		return err
	}
	units := kept
	for addr, unit := range freshUnits {
		units[addr] = unit
	}

	for _, path := range ws.Tombstones() {
		workspace.DropTombstonedFile(units, pkgAt(ws, path.Dir()), path)
	}
	for path := range dirty {
		if unit, ok := units[pkgAt(ws, path.Dir())]; ok {
			unit.MarkDirty(path)
		}
	}
	ws.SwapLoaded(newFset, units, narrow)
	return nil
}

// changedSet is the union of dirty files and tombstoned paths.
func changedSet(ws *workspace.Workspace) map[address.RelativePath]bool {
	out := make(map[address.RelativePath]bool)
	for _, addr := range ws.UnitKeys() {
		unit, _ := ws.Unit(addr)
		for _, pkg := range []*workspace.Package{unit.Prod(), unit.XTest()} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if file.IsDirty() {
					out[file.Path] = true
				}
			}
		}
	}
	for _, path := range ws.Tombstones() {
		out[path] = true
	}
	return out
}

// pkgAt wraps a workspace directory into its canonical package address.
func pkgAt(ws *workspace.Workspace, dir address.RelativePath) address.PkgPath {
	if dir == "." {
		return ws.Module()
	}
	return address.PkgPath(string(ws.Module()) + "/" + string(dir))
}
