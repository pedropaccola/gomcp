package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine/workspace"
)

// Flush writes every dirty file to disk, unlinks tombstoned paths, and
// clears both marks — the only place the mutation layer touches the
// filesystem.
func (e *Engine) Flush() (written, removed []address.RelativePath, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, addr := range e.ws.UnitKeys() {
		unit, _ := e.ws.Unit(addr)
		for _, pkg := range []*workspace.Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if !file.Dirty() {
					continue
				}
				abs := e.absPath(file.Path)
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return written, removed, err
				}
				if err := os.WriteFile(abs, file.Src(), 0o644); err != nil {
					return written, removed, err
				}
				file.MarkFlushed()
				written = append(written, file.Path)
			}
		}
	}
	for _, path := range e.ws.Tombstones() {
		if err := os.Remove(e.absPath(path)); err != nil && !os.IsNotExist(err) {
			return written, removed, err
		}
		e.ws.ClearTombstone(path)
		removed = append(removed, path)
	}
	return written, removed, nil
}

// Reload rebuilds the workspace from disk, discarding every unflushed
// change: the disk-facing inverse of Flush. It reports the files whose
// in-memory state was lost — dirty files and pending removals. An error
// means the previous state is untouched.
func (e *Engine) Reload(ctx context.Context) ([]address.RelativePath, error) {
	e.mu.RLock()
	var discarded []address.RelativePath
	for _, addr := range e.ws.UnitKeys() {
		unit, _ := e.ws.Unit(addr)
		for _, pkg := range []*workspace.Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if file.Dirty() {
					discarded = append(discarded, file.Path)
				}
			}
		}
	}
	discarded = append(discarded, e.ws.Tombstones()...)
	e.mu.RUnlock()
	slices.Sort(discarded)
	discarded = slices.Compact(discarded)
	if err := e.Bootstrap(ctx); err != nil {
		return nil, err
	}
	return discarded, nil
}
