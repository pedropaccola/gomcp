package workspace

import (
	"fmt"
	"go/token"

	"github.com/pedropaccola/gomcp/internal/address"
)

// ComputeEditPlan reports the facts EditSymbol needs about key's current
// declaration: whether it's part of a position-dependent group (in which
// case a replacement must resubmit the whole group), the group's token
// kind when grouped (token.ILLEGAL otherwise), and the Splice the
// replacement itself lands in. Aggregate-owned analysis, same rationale
// as DetectMoveConflicts: key is resolved fresh here.
func (w *Workspace) ComputeEditPlan(pkg address.PkgPath, key string) (wasPositionDependent bool, groupTok token.Token, target Splice, err error) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return false, token.ILLEGAL, Splice{}, fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	gen, grouped := sym.GroupOf()
	wasPositionDependent = constPositionDependent(gen, grouped, sym)
	var sp span
	var spanOK bool
	switch {
	case wasPositionDependent:
		sp, spanOK = w.declSpan(owner, sym)
	case grouped:
		sp, spanOK = w.specSpan(owner, sym)
	default:
		sp, spanOK = w.declSpan(owner, sym)
	}
	if !spanOK {
		return wasPositionDependent, token.ILLEGAL, Splice{}, fmt.Errorf("cannot locate %q in source", key)
	}
	tok := token.ILLEGAL
	if grouped {
		tok = gen.Tok
	}
	target, ok = w.NewSpliceAtOffset(owner, sym.File, sp.start, sp.end, nil)
	if !ok {
		return wasPositionDependent, token.ILLEGAL, Splice{}, fmt.Errorf("cannot locate %q in source", key)
	}
	return wasPositionDependent, tok, target, nil
}

// DetectEditCollisions reports which of newKeys already exist elsewhere in
// pkg, blocking a replacement of key — a same-group sibling doesn't
// count when key is itself position-dependent, since resubmitting the
// whole group necessarily re-mentions every member.
func (w *Workspace) DetectEditCollisions(pkg address.PkgPath, key string, newKeys []string) []string {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return nil
	}
	gen, grouped := sym.GroupOf()
	wasPositionDependent := constPositionDependent(gen, grouped, sym)
	var collisions []string
	for _, newKey := range newKeys {
		if newKey == key || newKey == "init" {
			continue
		}
		existing, exists := owner.Symbol(newKey)
		if !exists {
			continue
		}
		if wasPositionDependent {
			if eGen, eGrouped := existing.GroupOf(); eGrouped && eGen == gen {
				continue
			}
		}
		collisions = append(collisions, newKey)
	}
	return collisions
}
