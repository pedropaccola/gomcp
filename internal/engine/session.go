package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// Flush writes every dirty file to disk, unlinks tombstoned paths, and
// clears both marks — the only place the mutation layer touches the
// filesystem. Built against a private clone and published with one Store,
// same as Edit: a failure partway through discards the whole clone, so
// nothing appears flushed even if some files already reached disk —
// re-flushing recovers, at the cost of re-writing what already landed.
func (e *Engine) Flush() (written, removed []address.RelativePath, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	candidate := e.ws.Load().Clone()
	for _, addr := range candidate.UnitKeys() {
		unit, _ := candidate.Unit(addr)
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
				pkg.MarkFlushed(file.Path)
				written = append(written, file.Path)
			}
		}
	}
	for _, path := range candidate.Tombstones() {
		if err := os.Remove(e.absPath(path)); err != nil && !os.IsNotExist(err) {
			return written, removed, err
		}
		candidate.ClearTombstone(path)
		removed = append(removed, path)
	}
	e.ws.Store(candidate)
	return written, removed, nil
}

// Reload rebuilds the workspace from disk, discarding every unflushed
// change: the disk-facing inverse of Flush. It reports the files whose
// in-memory state was lost — dirty files and pending removals. An error
// means the previous state is untouched.
func (e *Engine) Reload(ctx context.Context) ([]address.RelativePath, error) {
	fset, module, units, err := e.load(ctx, nil)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	orig := e.ws.Load()
	var discarded []address.RelativePath
	for _, addr := range orig.UnitKeys() {
		unit, _ := orig.Unit(addr)
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
	discarded = append(discarded, orig.Tombstones()...)
	slices.Sort(discarded)
	discarded = slices.Compact(discarded)

	ws := workspace.NewWorkspace()
	ws.Reset(module, fset, units)
	e.ws.Store(ws)
	return discarded, nil
}
