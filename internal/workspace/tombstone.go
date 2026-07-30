package workspace

import (
	"maps"
	"slices"
)

// tombstoneMask is the overlay mask for a removed file: syntactically
// valid, semantically empty, so rechecks see the deletion's blast radius.
func tombstoneMask(pkgName string) []byte {
	return []byte("package " + pkgName + "\n")
}

// Tombstone masks a path as removed for the next recheck and flush,
// recording the package that owned it so later consumers (the recheck's
// tombstone sweep) never have to re-derive it from the path.
func (w *Workspace) Tombstone(pkg PackagePath, path FilePath, pkgName string) {
	w.ensureRemovedForked()
	w.removed[path] = tombstoneEntry{pkg: pkg, mask: tombstoneMask(pkgName)}
}

// ClearTombstone lifts a pending removal — a path recreated or moved onto
// is alive again.
func (w *Workspace) ClearTombstone(path FilePath) {
	w.ensureRemovedForked()
	delete(w.removed, path)
}

// TombstoneMask reports a path's overlay mask when it is pending removal.
func (w *Workspace) TombstoneMask(path FilePath) ([]byte, bool) {
	entry, ok := w.removed[path]
	return entry.mask, ok
}

// Tombstones enumerates every path pending removal, sorted.
func (w *Workspace) Tombstones() []FilePath {
	return slices.Sorted(maps.Keys(w.removed))
}

// TombstonePkg reports the package that owned path at the moment it was
// tombstoned — recorded at creation time so callers walking Tombstones
// never have to re-derive it from the path.
func (w *Workspace) TombstonePkg(path FilePath) (PackagePath, bool) {
	entry, ok := w.removed[path]
	return entry.pkg, ok
}

// tombstoneEntry is one pending removal: the package that owned the path
// when it was tombstoned, and the overlay mask that hides its content
// from rechecks until Flush unlinks it.
type tombstoneEntry struct {
	pkg  PackagePath
	mask []byte
}
