package workspace

import (
	"fmt"
	"go/ast"
	"go/token"
)

// isSoloGroup reports whether sym is ungrouped, or the only member of its
// parenthesized group.
func isSoloGroup(gen *ast.GenDecl, grouped bool) bool {
	return !grouped || len(gen.Specs) == 1
}

// GroupUsesIota reports whether any value expression in a grouped
// declaration references iota, making member meaning position-dependent.
// Exported so store shares this instead of keeping its own copy — a pure
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
	return w.offsetSpan(pkg, file, start, sym.Decl().End())
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
	return w.offsetSpan(pkg, file, start, sym.Spec().End())
}

// ExtractDeclaration returns key's declaration as standalone source together
// with the Splice its removal applies, doc comment included in both. A
// member of a grouped declaration with siblings is rebuilt ungrouped —
// doc first, then the group's keyword before the spec. Extraction refuses
// names sharing a spec (X, Y = 1, 2 — no way to split them). A
// const-group member whose value is taken from its position (iota, or
// inheriting the previous spec's expression) is extracted as the *whole*
// group instead — the same declSpan path a solo-member group already
// uses — since pulling just one member out would break the positions of
// the rest. Aggregate-owned analysis, same rationale as
// DetectMoveConflicts: key is resolved fresh here, not accepted as a
// pointer a caller might already be holding.
func (w *Workspace) ExtractDeclaration(pkg PackagePath, key string) (string, Splice, error) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return "", Splice{}, NoSymbolError(key, pkg)
	}
	if spec, ok := sym.Spec().(*ast.ValueSpec); ok && len(spec.Names) > 1 {
		return "", Splice{}, fmt.Errorf("%q is declared together with other names: replace the spec instead", sym.Key())
	}
	file, ok := owner.File(sym.File)
	if !ok {
		return "", Splice{}, errNotInSource(key)
	}
	gen, grouped := sym.GroupOf()
	if isSoloGroup(gen, grouped) || constPositionDependent(gen, grouped, sym) {
		sp, ok := w.declSpan(owner, sym)
		if !ok {
			return "", Splice{}, errNotInSource(key)
		}
		splice, ok := w.NewSpliceAtOffset(owner, sym.File, sp.start, sp.end, nil)
		if !ok {
			return "", Splice{}, errNotInSource(key)
		}
		return string(file.Src()[sp.start:sp.end]), splice, nil
	}
	sp, ok := w.specSpan(owner, sym)
	if !ok {
		return "", Splice{}, errNotInSource(key)
	}
	body, ok := w.offsetSpan(owner, file, sym.Spec().Pos(), sym.Spec().End())
	if !ok {
		return "", Splice{}, errNotInSource(key)
	}
	doc := string(file.Src()[sp.start:body.start])
	splice, ok := w.NewSpliceAtOffset(owner, sym.File, sp.start, sp.end, nil)
	if !ok {
		return "", Splice{}, errNotInSource(key)
	}
	return doc + gen.Tok.String() + " " + string(file.Src()[body.start:body.end]), splice, nil
}

// PositionDependentGroupMembers returns every key that must move or
// extract together with key: itself alone, unless key is a member of a
// grouped const declaration whose meaning is position-dependent (iota,
// or inheriting the previous spec's expression) — in which case every
// member of that group is included, the same set ExtractDeclaration
// itself acts on for such a member (see its own doc comment), so a
// safety check never disagrees with what extraction actually does.
// Deliberately narrow: var and type groups, and non-position-dependent
// const groups, are grouped in source for readability only — nothing
// about them requires moving together, so they are never expanded here.
func (w *Workspace) PositionDependentGroupMembers(pkg PackagePath, key string) ([]string, error) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return nil, NoSymbolError(key, pkg)
	}
	gen, grouped := sym.GroupOf()
	if !grouped || (!isSoloGroup(gen, grouped) && !constPositionDependent(gen, grouped, sym)) {
		return []string{key}, nil
	}
	var members []string
	for _, s := range owner.Symbols() {
		if g, ok := s.GroupOf(); ok && g == gen {
			members = append(members, s.Key())
		}
	}
	return members, nil
}

// GroupOf reports whether s lives inside a grouped declaration
// (const/var/type block with parentheses) and returns that declaration.
// Exported so store shares this instead of keeping its own copy — a
// pure method over Symbol's own already-exported Decl(), no
// workspace-internal state involved.
func (s *Symbol) GroupOf() (*ast.GenDecl, bool) {
	gen, ok := s.Decl().(*ast.GenDecl)
	return gen, ok && gen.Lparen.IsValid()
}
