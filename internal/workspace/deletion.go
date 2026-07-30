package workspace

import (
	"go/ast"
	"go/token"
)

// trimRange reports the byte range removing the idx'th element of a
// comma-separated list collapses to — from the previous element's end (or
// this element's own start, when it's first) through this element's end.
func trimRange[T ast.Node](elems []T, idx int) (token.Pos, token.Pos) {
	if idx == 0 {
		return elems[0].Pos(), elems[1].Pos()
	}
	return elems[idx-1].End(), elems[idx].End()
}

// trimSpecName computes the splices removing key from a multi-name
// ValueSpec (`var a, b int`, `var a, b = f()`), or ok=false when no other
// name in the spec is real (non-blank) — the caller then falls through to
// whole-span removal instead, since nothing would be left to preserve.
// Names with one value each (or none at all) have the targeted name, and
// its paired value if any, trimmed from their lists; names sharing one
// multi-valued expression can't shrink the call's arity, so the targeted
// name blanks to `_` instead — the only transform leaving every other
// name's behavior unaffected.
func (w *Workspace) trimSpecName(owner *Package, sym *Symbol, spec *ast.ValueSpec, key string) ([]Splice, bool) {
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
		splice, ok := w.NewSpliceAtPos(owner, sym.File, spec.Names[idx].Pos(), spec.Names[idx].End(), []byte("_"))
		if !ok {
			return nil, false
		}
		return []Splice{splice}, true
	}
	nameStart, nameEnd := trimRange(spec.Names, idx)
	nameSplice, ok := w.NewSpliceAtPos(owner, sym.File, nameStart, nameEnd, nil)
	if !ok {
		return nil, false
	}
	splices := []Splice{nameSplice}
	if len(spec.Values) == len(spec.Names) {
		valStart, valEnd := trimRange(spec.Values, idx)
		valSplice, ok := w.NewSpliceAtPos(owner, sym.File, valStart, valEnd, nil)
		if !ok {
			return nil, false
		}
		splices = append(splices, valSplice)
	}
	return splices, true
}

// ComputeDeletionSplices computes the splices removing key's declaration — its
// spec alone when it lives in a grouped declaration with siblings,
// unless its value is derived from its position (iota, or inheriting the
// previous spec's expression), in which case the whole group is removed
// together. A name sharing a spec with other real names is trimmed from
// it instead of taking the others down with it (trimSpecName). found
// false means key doesn't exist — deletion is idempotent, a caller should
// treat that as success, not an error. Aggregate-owned analysis, same
// rationale as DetectMoveConflicts: key is resolved fresh here.
func (w *Workspace) ComputeDeletionSplices(pkg PackagePath, key string) (splices []Splice, found bool, err error) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return nil, false, nil
	}
	gen, grouped := sym.GroupOf()
	if !constPositionDependent(gen, grouped, sym) {
		if spec, ok := sym.Spec().(*ast.ValueSpec); ok && len(spec.Names) > 1 {
			if sp, ok := w.trimSpecName(owner, sym, spec, key); ok {
				return sp, true, nil
			}
		}
	}
	sp, ok := w.declSpan(owner, sym)
	if !isSoloGroup(gen, grouped) && !constPositionDependent(gen, grouped, sym) {
		sp, ok = w.specSpan(owner, sym)
	}
	if !ok {
		return nil, true, errNotInSource(key)
	}
	splice, ok := w.NewSpliceAtOffset(owner, sym.File, sp.start, sp.end, nil)
	if !ok {
		return nil, true, errNotInSource(key)
	}
	return []Splice{splice}, true, nil
}
