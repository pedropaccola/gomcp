package workspace

import (
	"fmt"
	"go/token"
	"slices"
)

// ComputeEditPlan reports the facts EditSymbol needs about key's current
// declaration: whether it's part of a position-dependent group (in which
// case a replacement must resubmit the whole group), the group's token
// kind when grouped (token.ILLEGAL otherwise), and the ByteSplice the
// replacement itself lands in. Aggregate-owned analysis, same rationale
// as DetectMoveConflicts: key is resolved fresh here.
func (w *Workspace) ComputeEditPlan(pkg PackagePath, key string) (wasPositionDependent bool, groupTok token.Token, target ByteSplice, err error) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return false, token.ILLEGAL, ByteSplice{}, NoSymbolError(key, pkg)
	}
	gen, grouped := sym.GroupOf()
	wasPositionDependent = constPositionDependent(gen, grouped, sym)
	var sp ByteRange
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
		return wasPositionDependent, token.ILLEGAL, ByteSplice{}, errNotInSource(key)
	}
	tok := token.ILLEGAL
	if grouped {
		tok = gen.Tok
	}
	target, ok = w.NewSpliceAtOffset(owner, sym.File, sp, nil)
	if !ok {
		return wasPositionDependent, token.ILLEGAL, ByteSplice{}, errNotInSource(key)
	}
	return wasPositionDependent, tok, target, nil
}

// DetectEditCollisions reports which of newKeys already exist elsewhere in
// pkg, blocking a replacement of key — a same-group sibling doesn't
// count when key is itself position-dependent, since resubmitting the
// whole group necessarily re-mentions every member.
func (w *Workspace) DetectEditCollisions(pkg PackagePath, key string, newKeys []string) []string {
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

// EditSymbol replaces key's whole declaration with src — for members of
// grouped declarations, src is the member's spec as written inside the
// group. A replacement may rename; the new key must not collide.
// For a member of a position-dependent const group (iota, or inheriting
// the previous spec's expression), src must be the group's whole
// intended state — every member's spec, not just key's own, still bare
// (no group keyword/parens — those are reconstructed) — since a partial
// replacement would silently drop whatever isn't mentioned; the
// targeted key itself must still be present, or the edit is refused (use
// MoveSymbol to rename a group member instead, which propagates
// references correctly and is the only tool that can). Editing a
// non-position-dependent group member to introduce iota is refused —
// that converts the group's structure, not just one value, and isn't
// supported through a single member's replacement. Returns the file
// touched, for the caller's own change-tracking.
func (w *Workspace) EditSymbol(pkg PackagePath, key, src string) (FilePath, error) {
	wasPositionDependent, groupTok, target, err := w.ComputeEditPlan(pkg, key)
	if err != nil {
		return "", err
	}
	var frag Fragment
	replacement := src
	switch {
	case wasPositionDependent:
		frag, err = parseSpecFragment(groupTok, src)
		replacement = groupTok.String() + " (\n" + src + "\n)"
	case groupTok != token.ILLEGAL:
		frag, err = parseSpecFragment(groupTok, src)
		if err == nil && frag.UsesIota {
			err = fmt.Errorf("%q would introduce iota into a group that doesn't use it: converting a plain group into a position-dependent one isn't supported through a single member's replacement", key)
		}
	default:
		frag, err = parseDeclFragment(src)
	}
	if err != nil {
		return "", err
	}
	if wasPositionDependent && !slices.Contains(frag.Keys, key) {
		return "", fmt.Errorf("%q is missing from the replacement: a position-dependent group member can't be renamed through a single member's replacement", key)
	}
	if collisions := w.DetectEditCollisions(pkg, key, frag.Keys); len(collisions) > 0 {
		return "", fmt.Errorf("replacement declares %q, which already exists in %q", collisions[0], pkg)
	}
	file, owner, ok := w.ResolveFileByPath(target.Path)
	if !ok {
		return "", VanishedError(target.Path, fmt.Sprintf("while editing %q", key))
	}
	target.Repl = []byte(replacement)
	if err := w.SwapFile(pkg, owner.ID.Kind() == KindXTest, target.Path, ByteSplices{target}.Apply(file.Src())); err != nil {
		return "", err
	}
	return target.Path, nil
}
