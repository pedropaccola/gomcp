package gate

import (
	"fmt"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// DeleteFile removes one file and every declaration in it, tombstoning the
// path for Flush. Deletion is idempotent: a missing package or file is a
// noop, not a failure — the file being gone is the success condition,
// whoever caused it.
func (tx *Tx) DeleteFile(pkg address.PkgPath, name string) error {
	unit, ok := tx.ws.Unit(pkg)
	if !ok {
		return nil
	}
	for _, owner := range unit.Members() {
		path, err := address.NewFilePath(tx.ws.Module(), owner.PkgPath, name)
		if err != nil {
			return err
		}
		if _, ok := owner.File(path); !ok {
			continue
		}
		tx.ws.DropFile(pkg, owner == unit.XTest(), path)
		tx.markChanged(path)
		return nil
	}
	return nil
}

// DeletePackage removes a whole package address, tombstoning every file.
// Deletion is idempotent: a missing package is a noop, not a failure.
func (tx *Tx) DeletePackage(pkg address.PkgPath) error {
	unit, ok := tx.ws.Unit(pkg)
	if !ok {
		return nil
	}
	for _, p := range unit.Members() {
		for _, file := range p.Files() {
			tx.ws.Tombstone(pkg, file.Path, p.Name)
			tx.markChanged(file.Path)
		}
	}
	tx.ws.RemoveUnit(pkg)
	return nil
}

// DeleteSymbol removes key's declaration — its spec alone when it lives in
// a grouped declaration with siblings, unless its value is derived from
// its position (iota, or inheriting the previous spec's expression), in
// which case the whole group is removed together. Deleting one member of
// a position-dependent group and leaving the rest as-is has no single
// correct resolution (keep everyone else's original values? renumber
// them?) — that's edit_symbol's job, via a whole-group replacement that
// states explicitly what the agent wants, not a guess this verb would
// have to make.
//
// A name sharing a *ast.ValueSpec with others (`var a, b int`, `var a, b
// = f()`) is trimmed from the spec instead of taking the others down with
// it — see Workspace.DeletionSplices. Once no real name remains, the spec
// collapses to a full removal, same as deleting a solo name.
//
// Deletion is idempotent: a missing symbol is a noop, not a failure.
func (tx *Tx) DeleteSymbol(pkg address.PkgPath, key string) error {
	splices, found, err := tx.ws.ComputeDeletionSplices(pkg, key)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	path := splices[0].Path
	file, owner, ok := tx.ws.ResolveFileByPath(path)
	if !ok {
		return fmt.Errorf("internal error: %q vanished while deleting %q", path, key)
	}
	return tx.installFile(pkg, tx.ws.IsXTestOwner(pkg, owner), path, workspace.ApplySplices(file.Src(), splices))
}
