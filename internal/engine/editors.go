package engine

import (
	"fmt"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
)

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
// supported through a single member's replacement.
func (tx *Tx) EditSymbol(pkg address.PkgPath, key, src string) error {
	sym, owner, ok := tx.resolveSymbol(pkg, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	gen, grouped := groupOf(sym)
	wasPositionDependent := constPositionDependent(gen, grouped, sym)

	var frag fragment
	var sp span
	var spanOK bool
	var err error
	replacement := src
	switch {
	case wasPositionDependent:
		frag, err = parseSpecFragment(gen.Tok, src)
		sp, spanOK = tx.declSpan(sym)
		replacement = gen.Tok.String() + " (\n" + src + "\n)"
	case grouped:
		frag, err = parseSpecFragment(gen.Tok, src)
		if err == nil && frag.usesIota {
			err = fmt.Errorf("%q would introduce iota into a group that doesn't use it: converting a plain group into a position-dependent one isn't supported through edit_symbol", key)
		}
		sp, spanOK = tx.specSpan(sym)
	default:
		frag, err = parseDeclFragment(src)
		sp, spanOK = tx.declSpan(sym)
	}
	if err != nil {
		return err
	}
	if !spanOK {
		return fmt.Errorf("cannot locate %q in source", key)
	}
	if wasPositionDependent && !slices.Contains(frag.keys, key) {
		return fmt.Errorf("%q is missing from the replacement: a position-dependent group member can't be renamed through edit_symbol, use move_symbol instead", key)
	}
	for _, newKey := range frag.keys {
		if newKey == key || newKey == "init" {
			continue
		}
		existing, exists := owner.Symbol(newKey)
		if !exists {
			continue
		}
		if wasPositionDependent {
			if eGen, eGrouped := groupOf(existing); eGrouped && eGen == gen {
				continue // an existing sibling in the same group, expected when resubmitting it whole
			}
		}
		return fmt.Errorf("replacement declares %q, which already exists in %q", newKey, pkg)
	}
	file, _ := owner.File(sym.File)
	return tx.reloadFile(owner, sym.File, applySplices(file.Src(), []splice{{span: sp, repl: []byte(replacement)}}))
}

// EditFile replaces or clears a file's package doc comment — the comment
// block directly above the package clause — leaving the rest of the file
// untouched. The one sanctioned door into floating-comment space: every
// other comment stays unaddressable by design.
func (tx *Tx) EditFile(pkg address.PkgPath, name, doc string) error {
	p, ok := tx.resolvePackage(pkg)
	if !ok {
		return fmt.Errorf("no package at %q", pkg)
	}
	path, err := fileAddress(p, name)
	if err != nil {
		return err
	}
	file, _, ok := tx.resolveFile(path)
	if !ok {
		return fmt.Errorf("no file %q in %q", name, pkg)
	}
	astFile := file.Ast()
	docPos, docEnd := astFile.Package, astFile.Package
	if astFile.Doc != nil {
		docPos = astFile.Doc.Pos()
	}
	docSpan, ok := tx.offsetSpan(path, docPos, docEnd)
	if !ok {
		return fmt.Errorf("cannot locate doc comment span in %q", path)
	}
	candidate := applySplices(file.Src(), []splice{{span: docSpan, repl: renderDocComment(doc)}})
	return tx.reloadFile(p, path, candidate)
}
