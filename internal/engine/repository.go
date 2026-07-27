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
// filesystem. Built against a private clone and published with one
// assignment, same as Edit: a failure partway through discards the whole
// clone, so nothing appears flushed even if some files already reached
// disk — re-flushing recovers, at the cost of re-writing what already
// landed.
func (e *Engine) Flush() (written, removed []address.FilePath, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	candidate := e.ws.Clone()
	for _, addr := range candidate.UnitKeys() {
		unit, _ := candidate.Unit(addr)
		for i, pkg := range []*workspace.Package{unit.Prod(), unit.XTest()} {
			if pkg == nil {
				continue
			}
			isXTest := i == 1
			for _, file := range pkg.Files() {
				if !file.IsDirty() {
					continue
				}
				abs := e.absPath(file.Path)
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return written, removed, err
				}
				if err := os.WriteFile(abs, file.Src(), 0o644); err != nil {
					return written, removed, err
				}
				candidate.MarkFlushed(addr, isXTest, file.Path)
				written = append(written, file.Path)
			}
		}
	}
	for _, path := range candidate.Tombstones() {
		abs := e.absPath(path)
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return written, removed, err
		}
		candidate.ClearTombstone(path)
		removed = append(removed, path)
		e.removeEmptyAncestors(filepath.Dir(abs))
	}
	e.ws = candidate
	return written, removed, nil
}

// Reload rebuilds the workspace from disk, discarding every unflushed
// change: the disk-facing inverse of Flush. It reports the files whose
// in-memory state was lost — dirty files and pending removals. An error
// means the previous state is untouched.
func (e *Engine) Reload(ctx context.Context) ([]address.FilePath, error) {
	fset, module, units, err := e.load(ctx, nil)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	orig := e.ws
	var discarded []address.FilePath
	for _, addr := range orig.UnitKeys() {
		unit, _ := orig.Unit(addr)
		for _, pkg := range []*workspace.Package{unit.Prod(), unit.XTest()} {
			if pkg == nil {
				continue
			}
			for _, file := range pkg.Files() {
				if file.IsDirty() {
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
	e.ws = ws
	return discarded, nil
}

// removeEmptyAncestors best-effort removes dir and each now-empty parent
// up to (not including) RootDir, stopping at the first non-empty or
// already-gone directory. A leftover empty directory is disk debris, not
// a modeled entity, so a failure here is silently swallowed rather than
// failing Flush.
func (e *Engine) removeEmptyAncestors(dir string) {
	root := filepath.Clean(e.RootDir)
	for dir = filepath.Clean(dir); dir != root && filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			return
		}
	}
}
