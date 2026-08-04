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
// replacement itself lands in. fileName follows DeclSource's own
// assertion-vs-primary-preference convention. Aggregate-owned analysis,
// same rationale as DetectMoveConflicts: key is resolved fresh here.
func (w *Workspace) ComputeEditPlan(pkg PackagePath, key, fileName string) (wasPositionDependent bool, groupTok token.Token, target ByteSplice, err error) {
	var sym *Symbol
	var owner *Package
	var ok bool
	if fileName != "" {
		sym, owner, ok = w.ResolveSymbolIn(pkg, key, fileName)
	} else {
		sym, owner, ok = w.ResolveSymbol(pkg, key)
	}
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
// fileName scopes resolution exactly to that file (an assertion, never a
// hint) — the only way to reach a declaration a same-named sibling
// elsewhere would otherwise shadow; empty keeps primary-preference
// resolution. For a member of a position-dependent const group (iota, or
// inheriting the previous spec's expression), src must be the group's
// whole intended state — every member's spec, not just key's own, still
// bare (no group keyword/parens — those are reconstructed) — since a
// partial replacement would silently drop whatever isn't mentioned; the
// targeted key itself must still be present, or the edit is refused (use
// MoveSymbol to rename a group member instead, which propagates
// references correctly and is the only tool that can). Editing a
// non-position-dependent group member to introduce iota is refused —
// that converts the group's structure, not just one value, and isn't
// supported through a single member's replacement. Returns the file
// touched and key's own directive-line delta (added, then removed,
// relative to key's directives before this edit — always computed, never
// gated on whether src's comment happens to mention directives at all),
// for the caller's own change-tracking and reporting. A rename looks up
// the delta under whatever new name src declares — key itself won't be
// in frag.SymbolDirectives anymore — which is safe even when a shared
// spec declares several names at once, since they all inherit the same
// spec-level doc comment and so carry identical directives.
func (w *Workspace) EditSymbol(pkg PackagePath, key, src, fileName string) (path FilePath, added, removed []string, err error) {
	wasPositionDependent, groupTok, target, err := w.ComputeEditPlan(pkg, key, fileName)
	if err != nil {
		return "", nil, nil, err
	}
	var oldSym *Symbol
	if fileName != "" {
		oldSym, _, _ = w.ResolveSymbolIn(pkg, key, fileName) // known to exist: ComputeEditPlan just resolved it
	} else {
		oldSym, _, _ = w.ResolveSymbol(pkg, key)
	}
	oldDirectives := oldSym.Directives
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
		return "", nil, nil, err
	}
	if wasPositionDependent && !slices.Contains(frag.Keys, key) {
		return "", nil, nil, fmt.Errorf("%q is missing from the replacement: a position-dependent group member can't be renamed through a single member's replacement", key)
	}
	if collisions := w.DetectEditCollisions(pkg, key, frag.Keys); len(collisions) > 0 {
		return "", nil, nil, fmt.Errorf("replacement declares %q, which already exists in %q", collisions[0], pkg)
	}
	file, owner, ok := w.ResolveFileByPath(target.Path)
	if !ok {
		return "", nil, nil, VanishedError(target.Path, fmt.Sprintf("while editing %q", key))
	}
	target.Repl = []byte(replacement)
	if err := w.SwapFile(pkg, owner.ID.Kind(), file.Ignored, target.Path, ByteSplices{target}.Apply(file.Src())); err != nil {
		return "", nil, nil, err
	}
	newKey := key
	if _, ok := frag.SymbolDirectives[key]; !ok && len(frag.Keys) > 0 {
		newKey = frag.Keys[0]
	}
	added, removed = DiffDirectives(oldDirectives, frag.SymbolDirectives[newKey])
	return target.Path, added, removed, nil
}

// EditFile replaces a file's leading region — its compiler directives and
// its package doc comment, the whole span from the first leading comment
// through the package clause — leaving the rest of the file untouched.
// doc == nil leaves the doc comment as-is; a non-nil doc (even "") sets
// or clears it. directives == nil leaves the directive block as-is; a
// non-nil directives (even empty) sets or clears it. Composes on
// File.EditHeader for the byte-level computation, then applies it via
// ReclassifyFile — editing a file's directives to add or remove a
// build-excluding one updates its Ignored bit in place; its shape
// (Prod/XTest) never changes, since a directive edit never changes a
// file's own package clause. Returns the file touched and the file's own
// directive-line delta (added, then removed, relative to its directives
// before this edit — always computed, never gated on whether directives
// was even given), for the caller's own change-tracking and reporting —
// the same shape EditSymbol already returns for a symbol's own delta.
func (w *Workspace) EditFile(pkg PackagePath, name string, doc *string, directives []string) (path FilePath, added, removed []string, err error) {
	path, err = NewFilePath(w.Module(), pkg, name)
	if err != nil {
		return "", nil, nil, err
	}
	file, owner, ok := w.ResolveFileByPath(path)
	if !ok {
		return "", nil, nil, NoFileError(name, pkg)
	}
	oldDirectives := file.Directives
	newDirectives := oldDirectives
	if directives != nil {
		newDirectives = directives
	}
	sp, ok := file.EditHeader(w.FsetOf(owner), doc, directives)
	if !ok {
		return "", nil, nil, fmt.Errorf("cannot locate leading comment span in %q", path)
	}
	candidate := ByteSplices{sp}.Apply(file.Src())
	if _, err := w.ReclassifyFile(pkg, path, owner.ID.Kind(), newDirectives, candidate); err != nil {
		return "", nil, nil, err
	}
	added, removed = DiffDirectives(oldDirectives, newDirectives)
	return path, added, removed, nil
}
