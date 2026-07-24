package engine

import (
	"context"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// recheckLocked reloads the workspace with the in-memory truth overlaid on
// disk and swaps the fresh state into ws, carrying dirty marks over and
// pruning tombstoned paths. ws is the caller's own candidate — recheckLocked
// never touches e.ws — and the caller still must hold e.mu, since ws is
// being built for eventual publication, not yet safe to read concurrently.
func (e *Engine) recheckLocked(ctx context.Context, ws *workspace.Workspace) error {
	view := &View{eng: e, ws: ws}
	overlay := make(map[string][]byte)
	dirty := make(map[address.RelativePath]bool)
	for path := range changedSet(ws) {
		if mask, tombstoned := ws.TombstoneMask(path); tombstoned {
			overlay[e.absPath(path)] = mask
			continue
		}
		if file, _, ok := view.resolveFile(path); ok {
			overlay[e.absPath(path)] = file.Src()
			dirty[path] = true
		}
	}

	fset, _, units, err := e.load(ctx, overlay)
	if err != nil {
		return err
	}
	for _, path := range ws.Tombstones() {
		workspace.PruneFile(units, view.pkgAt(path.Dir()), path)
	}
	for path := range dirty {
		if unit, ok := units[view.pkgAt(path.Dir())]; ok {
			unit.MarkDirty(path)
		}
	}
	ws.SwapLoaded(fset, units)
	return nil
}

// changedSet is the union of dirty files and tombstoned paths.
func changedSet(ws *workspace.Workspace) map[address.RelativePath]bool {
	out := make(map[address.RelativePath]bool)
	for _, addr := range ws.UnitKeys() {
		unit, _ := ws.Unit(addr)
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
	for _, path := range ws.Tombstones() {
		out[path] = true
	}
	return out
}
