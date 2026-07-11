package engine

import (
	"context"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine/workspace"
)

// changedSet is the union of dirty files and tombstoned paths.
func (e *Engine) changedSet() map[address.RelativePath]bool {
	out := make(map[address.RelativePath]bool)
	for _, addr := range e.ws.UnitKeys() {
		unit, _ := e.ws.Unit(addr)
		for _, pkg := range []*workspace.Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if file.Dirty() {
					out[file.Path] = true
				}
			}
		}
	}
	for _, path := range e.ws.Tombstones() {
		out[path] = true
	}
	return out
}

// recheckLocked reloads the workspace with the in-memory truth overlaid on
// disk and swaps the fresh state in, carrying dirty marks over and pruning
// tombstoned paths. Caller must hold the write lock.
func (e *Engine) recheckLocked(ctx context.Context) error {
	overlay := make(map[string][]byte)
	dirty := make(map[address.RelativePath]bool)
	for path := range e.changedSet() {
		if mask, tombstoned := e.ws.TombstoneMask(path); tombstoned {
			overlay[e.absPath(path)] = mask
			continue
		}
		if file, _, ok := (&View{eng: e}).resolveFile(path); ok {
			overlay[e.absPath(path)] = file.Src()
			dirty[path] = true
		}
	}

	fset, _, units, err := e.load(ctx, overlay)
	if err != nil {
		return err
	}
	for _, path := range e.ws.Tombstones() {
		workspace.PruneFile(units, e.pkgAt(path.Dir()), path)
	}
	for path := range dirty {
		if unit, ok := units[e.pkgAt(path.Dir())]; ok {
			unit.MarkDirty(path)
		}
	}
	e.ws.SwapLoaded(fset, units)
	return nil
}
