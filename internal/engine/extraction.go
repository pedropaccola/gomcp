package engine

import (
	"fmt"
	"go/ast"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// declSpan is the byte span of the whole declaration, doc comment included.
func (v *View) declSpan(sym *workspace.Symbol) (span, bool) {
	start := sym.Decl().Pos()
	if doc := workspace.DocOf(sym.Decl()); doc != nil {
		start = doc.Pos()
	}
	return v.offsetSpan(sym.File, start, sym.Decl().End())
}

// specSpan is the byte span of the symbol's own spec, doc included.
func (v *View) specSpan(sym *workspace.Symbol) (span, bool) {
	if sym.Spec() == nil {
		return v.declSpan(sym)
	}
	start := sym.Spec().Pos()
	if doc := workspace.DocOf(sym.Spec()); doc != nil {
		start = doc.Pos()
	}
	return v.offsetSpan(sym.File, start, sym.Spec().End())
}

// extractDecl returns sym's declaration as standalone source together with
// the span its removal splices out, doc comment included in both. A member
// of a grouped declaration with siblings is rebuilt ungrouped — doc first,
// then the group's keyword before the spec. Extraction refuses names
// sharing a spec (X, Y = 1, 2 — no way to split them). A const-group
// member whose value is taken from its position (iota, or inheriting the
// previous spec's expression) is extracted as the *whole* group instead —
// the same declSpan path a solo-member group already uses — since pulling
// just one member out would break the positions of the rest; MoveSymbol
// relocates every member together as a result, not just the one named.
func (tx *Tx) extractDecl(sym *workspace.Symbol, file *workspace.File) (string, span, error) {
	if spec, ok := sym.Spec().(*ast.ValueSpec); ok && len(spec.Names) > 1 {
		return "", span{}, fmt.Errorf("%q is declared together with other names: replace the spec instead", sym.Key())
	}
	gen, grouped := groupOf(sym)
	if soloGroup(gen, grouped) || constPositionDependent(gen, grouped, sym) {
		sp, ok := tx.declSpan(sym)
		if !ok {
			return "", span{}, fmt.Errorf("cannot locate %q in source", sym.Key())
		}
		return string(file.Src()[sp.start:sp.end]), sp, nil
	}
	sp, ok := tx.specSpan(sym)
	if !ok {
		return "", span{}, fmt.Errorf("cannot locate %q in source", sym.Key())
	}
	body, ok := tx.offsetSpan(sym.File, sym.Spec().Pos(), sym.Spec().End())
	if !ok {
		return "", span{}, fmt.Errorf("cannot locate %q in source", sym.Key())
	}
	doc := string(file.Src()[sp.start:body.start])
	return doc + gen.Tok.String() + " " + string(file.Src()[body.start:body.end]), sp, nil
}

// groupUsesIota reports whether any value expression in a grouped
// declaration references iota, making member meaning position-dependent.
func groupUsesIota(gen *ast.GenDecl) bool {
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
