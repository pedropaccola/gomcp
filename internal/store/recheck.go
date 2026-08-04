package store

import (
	"context"
	"go/token"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// recheckNarrowLocked reloads the workspace with the in-memory truth
// overlaid on disk, narrowed to dirty packages ∨ their transitive
// importers — see recheckScopedLocked for the mechanism. ws is the
// caller's own candidate — recheckNarrowLocked never touches e.ws — and
// the caller still must hold e.mu, since ws is being built for eventual
// publication, not yet safe to read concurrently.
func (e *Store) recheckNarrowLocked(ctx context.Context, ws *workspace.Workspace) error {
	return e.recheckScopedLocked(ctx, ws, false)
}

// recheckFullLocked is recheckNarrowLocked's full-module variant: every
// package is rebuilt fresh, nothing carried forward — the same shape a
// dirty-scoped recheck degenerates to when its scope happens to cover
// every package. Used by EnsureFullyChecked to restore a unified
// type-checking universe.
func (e *Store) recheckFullLocked(ctx context.Context, ws *workspace.Workspace) error {
	return e.recheckScopedLocked(ctx, ws, true)
}

func (e *Store) recheckScopedLocked(ctx context.Context, ws *workspace.Workspace, forceFull bool) error {
	overlay := make(map[string][]byte)
	dirty := make(map[workspace.FilePath]workspace.PackagePath)
	dirtyPkgs := make(map[workspace.PackagePath]bool)
	module := ws.Module()
	for path, pkg := range changedSet(ws) {
		dirtyPkgs[pkg] = true
		if mask, tombstoned := ws.TombstoneMask(path); tombstoned {
			overlay[e.AbsPath(module, path)] = mask
			continue
		}
		for _, p := range ws.MembersOf(pkg) {
			if file, ok := p.File(path); ok {
				overlay[e.AbsPath(module, path)] = file.Src()
				dirty[path] = pkg
			}
		}
	}

	scope := ws.ComputeRecheckScope(dirtyPkgs)
	if forceFull {
		scope = make(map[workspace.PackagePath]bool, len(ws.UnitKeys()))
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
	keptProd := make(map[workspace.PackagePath]*workspace.Package)
	keptXTest := make(map[workspace.PackagePath]*workspace.Package)
	for _, addr := range ws.UnitKeys() {
		members := ws.MembersOf(addr)
		if scope[addr] {
			for _, p := range members {
				for _, file := range p.Files() {
					if tf := oldFset.File(file.Ast().Pos()); tf != nil {
						oldFset.RemoveFile(tf)
					}
				}
			}
			continue
		}
		for _, p := range members {
			if p.ID.Kind() == workspace.KindXTest {
				keptXTest[addr] = p
			} else {
				keptProd[addr] = p
			}
			for _, file := range p.Files() {
				if tf := oldFset.File(file.Ast().Pos()); tf != nil {
					newFset.AddExistingFiles(tf)
				}
			}
		}
	}
	// Captured before the merge below: keptProd/keptXTest alias the same
	// maps merged into below (Go maps are reference types), so their
	// lengths after merging fresh results in would reflect the merged
	// total, not what was actually carried forward.
	narrow := len(keptProd) > 0 || len(keptXTest) > 0

	_, _, freshProd, freshXTest, err := e.LoadInto(ctx, newFset, overlay, patterns...)
	if err != nil {
		return err
	}
	prod, xtest := keptProd, keptXTest
	for addr, p := range freshProd {
		prod[addr] = p
	}
	for addr, p := range freshXTest {
		xtest[addr] = p
	}

	for _, path := range ws.Tombstones() {
		if pkg, ok := ws.TombstonePkg(path); ok {
			workspace.DropTombstonedFile(prod, xtest, pkg, path)
		}
	}
	for path, pkg := range dirty {
		workspace.MarkFileDirty(prod, xtest, pkg, path)
	}
	ws.Rebuild(newFset, prod, xtest, narrow)
	return nil
}

// changedSet is the union of dirty files and tombstoned paths, each
// paired with the directory-canonical package address that owns it (the
// key Workspace.units and go/packages patterns actually use — not
// necessarily a *workspace.Package's own .ID, which for an XTest half
// names its own distinct import path). Known already at every path's
// point of origin (the unit-key loop below, or the tombstone's own
// creation-time record), so callers never re-derive it from the path.
func changedSet(ws *workspace.Workspace) map[workspace.FilePath]workspace.PackagePath {
	out := make(map[workspace.FilePath]workspace.PackagePath)
	for ref, file := range ws.Files() {
		if file.IsDirty() {
			out[file.Path] = ref.Pkg
		}
	}
	for _, path := range ws.Tombstones() {
		if pkg, ok := ws.TombstonePkg(path); ok {
			out[path] = pkg
		}
	}
	return out
}
