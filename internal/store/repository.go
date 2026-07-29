package store

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// Flush writes every dirty file to disk, unlinks tombstoned paths, and
// clears both marks — the only place the mutation layer touches the
// filesystem. Built against a private clone and published with one
// assignment, same as Edit: a failure partway through discards the whole
// clone, so nothing appears flushed even if some files already reached
// disk — re-flushing recovers, at the cost of re-writing what already
// landed.
func (e *Store) Flush() (written, removed []address.FilePath, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	candidate := e.ws.Clone()
	module := candidate.Module()
	for ref, file := range candidate.Files() {
		if !file.IsDirty() {
			continue
		}
		abs := e.AbsPath(module, file.Path)
		if err := e.WriteFile(abs, file.Src()); err != nil {
			return written, removed, err
		}
		candidate.MarkFlushed(ref.Pkg, ref.IsXTest, file.Path)
		written = append(written, file.Path)
	}
	for _, path := range candidate.Tombstones() {
		abs := e.AbsPath(module, path)
		if err := e.RemoveFile(abs); err != nil {
			return written, removed, err
		}
		candidate.ClearTombstone(path)
		removed = append(removed, path)
		e.RemoveEmptyAncestors(filepath.Dir(abs))
	}
	e.ws = candidate
	return written, removed, nil
}

// Reload rebuilds the workspace from disk, discarding every unflushed
// change: the disk-facing inverse of Flush. It reports the files whose
// in-memory state was lost — dirty files and pending removals. An error
// means the previous state is untouched.
func (e *Store) Reload(ctx context.Context) ([]address.FilePath, error) {
	fset, module, units, err := e.Load(ctx, nil)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	orig := e.ws
	var discarded []address.FilePath
	for _, file := range orig.Files() {
		if file.IsDirty() {
			discarded = append(discarded, file.Path)
		}
	}
	discarded = append(discarded, orig.Tombstones()...)
	slices.Sort(discarded)
	discarded = slices.Compact(discarded)

	ws := workspace.NewWorkspace()
	ws.Reset(module, fset, units)
	e.ws = ws
	return discarded, nil
}
