package workspace

import (
	"fmt"
	"go/ast"
	"go/token"

	"github.com/pedropaccola/gomcp/internal/address"
)

// GroupOf reports whether sym lives inside a grouped declaration
// (const/var/type block with parentheses) and returns that declaration.
// Exported so gate shares this instead of keeping its own copy — a pure
// function over Symbol's own already-exported Decl(), no
// workspace-internal state involved.
func GroupOf(sym *Symbol) (*ast.GenDecl, bool) {
	gen, ok := sym.Decl().(*ast.GenDecl)
	return gen, ok && gen.Lparen.IsValid()
}

// isSoloGroup reports whether sym is ungrouped, or the only member of its
// parenthesized group.
func isSoloGroup(gen *ast.GenDecl, grouped bool) bool {
	return !grouped || len(gen.Specs) == 1
}

// GroupUsesIota reports whether any value expression in a grouped
// declaration references iota, making member meaning position-dependent.
// Exported so gate shares this instead of keeping its own copy — a pure
// function over the standard library's own *ast.GenDecl, no workspace
// state involved.
func GroupUsesIota(gen *ast.GenDecl) bool {
	found := false
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, value := range vs.Values {
			ast.Inspect(value, func(n ast.Node) bool {
				if ident, ok := n.(*ast.Ident); ok && ident.Name == "iota" {
					found = true
				}
				return !found
			})
		}
	}
	return found
}

// constPositionDependent reports whether sym's value is derived from its
// position in a grouped const declaration (iota, or inheriting the
// previous spec's expression).
func constPositionDependent(gen *ast.GenDecl, grouped bool, sym *Symbol) bool {
	if !grouped || gen.Tok != token.CONST {
		return false
	}
	spec, ok := sym.Spec().(*ast.ValueSpec)
	return ok && (len(spec.Values) == 0 || GroupUsesIota(gen))
}

// declSpan is the byte span of sym's whole declaration, doc comment
// included.
func (w *Workspace) declSpan(pkg *Package, sym *Symbol) (span, bool) {
	file, ok := pkg.File(sym.File)
	if !ok {
		return span{}, false
	}
	start := sym.Decl().Pos()
	if doc := DocOf(sym.Decl()); doc != nil {
		start = doc.Pos()
	}
	return offsetSpan(w, pkg, file, start, sym.Decl().End())
}

// specSpan is the byte span of sym's own spec, doc included.
func (w *Workspace) specSpan(pkg *Package, sym *Symbol) (span, bool) {
	if sym.Spec() == nil {
		return w.declSpan(pkg, sym)
	}
	file, ok := pkg.File(sym.File)
	if !ok {
		return span{}, false
	}
	start := sym.Spec().Pos()
	if doc := DocOf(sym.Spec()); doc != nil {
		start = doc.Pos()
	}
	return offsetSpan(w, pkg, file, start, sym.Spec().End())
}

// ExtractDecl returns key's declaration as standalone source together
// with the Splice its removal applies, doc comment included in both. A
// member of a grouped declaration with siblings is rebuilt ungrouped —
// doc first, then the group's keyword before the spec. Extraction refuses
// names sharing a spec (X, Y = 1, 2 — no way to split them). A
// const-group member whose value is taken from its position (iota, or
// inheriting the previous spec's expression) is extracted as the *whole*
// group instead — the same declSpan path a solo-member group already
// uses — since pulling just one member out would break the positions of
// the rest. Aggregate-owned analysis, same rationale as MoveConflicts:
// key is resolved fresh here, not accepted as a pointer a caller might
// already be holding.
func (w *Workspace) ExtractDecl(pkg address.PkgPath, key string) (string, Splice, error) {
	sym, owner, ok := w.resolveSymbol(pkg, key)
	if !ok {
		return "", Splice{}, fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	if spec, ok := sym.Spec().(*ast.ValueSpec); ok && len(spec.Names) > 1 {
		return "", Splice{}, fmt.Errorf("%q is declared together with other names: replace the spec instead", sym.Key())
	}
	file, ok := owner.File(sym.File)
	if !ok {
		return "", Splice{}, fmt.Errorf("cannot locate %q in source", key)
	}
	gen, grouped := GroupOf(sym)
	if isSoloGroup(gen, grouped) || constPositionDependent(gen, grouped, sym) {
		sp, ok := w.declSpan(owner, sym)
		if !ok {
			return "", Splice{}, fmt.Errorf("cannot locate %q in source", key)
		}
		return string(file.Src()[sp.start:sp.end]), Splice{Path: sym.File, Start: sp.start, End: sp.end}, nil
	}
	sp, ok := w.specSpan(owner, sym)
	if !ok {
		return "", Splice{}, fmt.Errorf("cannot locate %q in source", key)
	}
	body, ok := offsetSpan(w, owner, file, sym.Spec().Pos(), sym.Spec().End())
	if !ok {
		return "", Splice{}, fmt.Errorf("cannot locate %q in source", key)
	}
	doc := string(file.Src()[sp.start:body.start])
	return doc + gen.Tok.String() + " " + string(file.Src()[body.start:body.end]), Splice{Path: sym.File, Start: sp.start, End: sp.end}, nil
}

// PositionDependentGroupMembers returns every key that must move or
// extract together with key: itself alone, unless key is a member of a
// grouped const declaration whose meaning is position-dependent (iota,
// or inheriting the previous spec's expression) — in which case every
// member of that group is included, since ExtractDecl already promotes
// such an extraction to the whole group and any safety check must see
// the same set ExtractDecl is about to act on. Deliberately narrow:
// var and type groups, and non-position-dependent const groups, are
// grouped in source for readability only — nothing about them requires
// moving together, so they are never expanded here.
func (w *Workspace) PositionDependentGroupMembers(pkg address.PkgPath, key string) ([]string, error) {
	sym, owner, ok := w.resolveSymbol(pkg, key)
	if !ok {
		return nil, fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	gen, grouped := GroupOf(sym)
	if !grouped || (!isSoloGroup(gen, grouped) && !constPositionDependent(gen, grouped, sym)) {
		return []string{key}, nil
	}
	var members []string
	for _, s := range owner.Symbols() {
		if g, ok := GroupOf(s); ok && g == gen {
			members = append(members, s.Key())
		}
	}
	return members, nil
}
