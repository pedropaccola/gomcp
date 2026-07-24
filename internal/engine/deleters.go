package engine

import (
	"fmt"
	"go/ast"
	"go/token"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

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
// it — see trimSpecName. Once no real name remains, the spec collapses to
// a full removal, same as deleting a solo name.
//
// Deletion is idempotent: a missing symbol is a noop, not a failure.
func (tx *Tx) DeleteSymbol(pkg address.PkgPath, key string) error {
	sym, owner, ok := tx.resolveSymbol(pkg, key)
	if !ok {
		return nil
	}
	gen, grouped := groupOf(sym)
	if !constPositionDependent(gen, grouped, sym) {
		if spec, ok := sym.Spec().(*ast.ValueSpec); ok && len(spec.Names) > 1 {
			if splices, ok := tx.trimSpecName(sym, spec, key); ok {
				file, _ := owner.File(sym.File)
				return tx.reloadFile(owner, sym.File, applySplices(file.Src(), splices))
			}
		}
	}
	sp, ok := tx.declSpan(sym)
	if !soloGroup(gen, grouped) && !constPositionDependent(gen, grouped, sym) {
		sp, ok = tx.specSpan(sym)
	}
	if !ok {
		return fmt.Errorf("cannot locate %q in source", key)
	}
	file, _ := owner.File(sym.File)
	return tx.reloadFile(owner, sym.File, applySplices(file.Src(), []splice{{span: sp}}))
}

// DeleteFile removes one file and every declaration in it, tombstoning the
// path for Flush. Deletion is idempotent: a missing package or file is a
// noop, not a failure — the file being gone is the success condition,
// whoever caused it.
func (tx *Tx) DeleteFile(pkg address.PkgPath, name string) error {
	unit, ok := tx.eng.ws.Unit(pkg)
	if !ok {
		return nil
	}
	for _, owner := range []*workspace.Package{unit.Prod, unit.XTest} {
		if owner == nil {
			continue
		}
		path, err := fileAddress(owner, name)
		if err != nil {
			return err
		}
		if _, ok := owner.File(path); !ok {
			continue
		}
		tx.eng.ws.DropFile(pkg, owner, path)
		tx.touch(path)
		return nil
	}
	return nil
}

// DeletePackage removes a whole package address, tombstoning every file.
// Deletion is idempotent: a missing package is a noop, not a failure.
func (tx *Tx) DeletePackage(pkg address.PkgPath) error {
	unit, ok := tx.eng.ws.Unit(pkg)
	if !ok {
		return nil
	}
	for _, p := range []*workspace.Package{unit.Prod, unit.XTest} {
		if p == nil {
			continue
		}
		for _, file := range p.Files() {
			tx.eng.ws.Tombstone(file.Path, p.Name)
			tx.touch(file.Path)
		}
	}
	tx.eng.ws.RemoveUnit(pkg)
	return nil
}

// trimSpecName computes the splices removing key from a multi-name
// ValueSpec (`var a, b int`, `var a, b = f()`), or reports false when no
// other name in the spec is real (non-blank) — the caller then falls
// through to whole-span removal instead, since nothing would be left to
// preserve. Names with one value each (or none at all) have the targeted
// name, and its paired value if any, trimmed from their lists; names
// sharing one multi-valued expression can't shrink the call's arity, so
// the targeted name blanks to `_` instead — the only transform leaving
// every other name's behavior unaffected.
func (tx *Tx) trimSpecName(sym *workspace.Symbol, spec *ast.ValueSpec, key string) ([]splice, bool) {
	idx := -1
	for i, n := range spec.Names {
		if n.Name == key {
			idx = i
			break
		}
	}
	real := false
	for i, n := range spec.Names {
		if i != idx && n.Name != "_" {
			real = true
			break
		}
	}
	if !real {
		return nil, false
	}
	if len(spec.Values) > 0 && len(spec.Values) < len(spec.Names) {
		sp, ok := tx.offsetSpan(sym.File, spec.Names[idx].Pos(), spec.Names[idx].End())
		if !ok {
			return nil, false
		}
		return []splice{{span: sp, repl: []byte("_")}}, true
	}
	nameStart, nameEnd := trimRange(spec.Names, idx)
	sp, ok := tx.offsetSpan(sym.File, nameStart, nameEnd)
	if !ok {
		return nil, false
	}
	splices := []splice{{span: sp}}
	if len(spec.Values) == len(spec.Names) {
		valStart, valEnd := trimRange(spec.Values, idx)
		vsp, ok := tx.offsetSpan(sym.File, valStart, valEnd)
		if !ok {
			return nil, false
		}
		splices = append(splices, splice{span: vsp})
	}
	return splices, true
}

// trimRange reports the byte range removing the idx'th element of a
// comma-separated list collapses to — from the previous element's end (or
// this element's own start, when it's first) through this element's end.
func trimRange[T ast.Node](elems []T, idx int) (token.Pos, token.Pos) {
	if idx == 0 {
		return elems[0].Pos(), elems[1].Pos()
	}
	return elems[idx-1].End(), elems[idx].End()
}
