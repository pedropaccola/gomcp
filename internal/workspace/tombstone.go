package workspace

import (
	"maps"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
)

// tombstoneMask is the overlay mask for a removed file: syntactically
// valid, semantically empty, so rechecks see the deletion's blast radius.
func tombstoneMask(pkgName string) []byte {
	return []byte("package " + pkgName + "\n")
}

// Tombstone masks a path as removed for the next recheck and flush.
func (w *Workspace) Tombstone(path address.RelativePath, pkgName string) {
	w.ensureRemovedForked()
	w.removed[path] = tombstoneMask(pkgName)
}

// ClearTombstone lifts a pending removal — a path recreated or moved onto
// is alive again.
func (w *Workspace) ClearTombstone(path address.RelativePath) {
	w.ensureRemovedForked()
	delete(w.removed, path)
}

// TombstoneMask reports a path's overlay mask when it is pending removal.
func (w *Workspace) TombstoneMask(path address.RelativePath) ([]byte, bool) {
	mask, ok := w.removed[path]
	return mask, ok
}

// Tombstones enumerates every path pending removal, sorted.
func (w *Workspace) Tombstones() []address.RelativePath {
	return slices.Sorted(maps.Keys(w.removed))
}
