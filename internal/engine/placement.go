package engine

import (
	"go/ast"
	"go/token"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// insertOffset returns the canonical insertion offset for a new declaration
// per the placement policy: const/var at the top after imports, types after
// values, funcs at the bottom, methods right after their receiver group. A
// method whose receiver group isn't in this file falls to the bottom.
func (tx *Tx) insertOffset(file *workspace.File, frag fragment) int {
	effective := frag
	if frag.kind == KindMethod && !hasReceiverAnchor(file.Ast(), frag.recv) {
		effective = fragment{kind: KindFunc}
	}
	var anchor ast.Decl
	for _, decl := range file.Ast().Decls {
		if declPrecedes(decl, effective) {
			anchor = decl
		}
	}
	if anchor == nil {
		// Nothing precedes: insert right after the package clause.
		if sp, ok := tx.offsetSpan(file.Path, file.Ast().Name.Pos(), file.Ast().Name.End()); ok {
			return sp.end
		}
		return len(file.Src())
	}
	if sp, ok := tx.offsetSpan(file.Path, anchor.Pos(), anchor.End()); ok {
		return sp.end
	}
	return len(file.Src())
}

// typeDeclOffset returns the insertion offset right after typeName's own
// declaration in file, or -1 when the type isn't declared there — the
// same "cluster with your type" placement declPrecedes already gives
// methods, extended to a typed iota group anchored to that type instead
// of a receiver. Falls back to the standard const/var region when it
// isn't found, same as a method whose receiver isn't in the file falls
// to the plain-func region.
func (tx *Tx) typeDeclOffset(file *workspace.File, typeName string) int {
	for _, decl := range file.Ast().Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE || !declaresType(gen, typeName) {
			continue
		}
		if sp, ok := tx.offsetSpan(file.Path, decl.Pos(), decl.End()); ok {
			return sp.end
		}
	}
	return -1
}

// declPrecedes reports whether decl belongs at or before the fragment's
// canonical region. Regions rank imports < values < types-with-their-methods
// < funcs; a method fragment anchors only to its own receiver group.
func declPrecedes(decl ast.Decl, frag fragment) bool {
	if frag.kind == KindMethod {
		switch d := decl.(type) {
		case *ast.GenDecl:
			return d.Tok == token.IMPORT || d.Tok == token.CONST || d.Tok == token.VAR ||
				(d.Tok == token.TYPE && declaresType(d, frag.recv))
		case *ast.FuncDecl:
			return d.Recv != nil && workspace.RecvTypeName(d.Recv) == frag.recv
		}
		return false
	}
	return declRegion(decl) <= kindRegion(frag.kind)
}

// declRegion places an existing declaration in the file's canonical region
// order: imports (0) < const/var (1) < types and their methods (2) < plain
// funcs (3). declPrecedes compares it against kindRegion.
func declRegion(decl ast.Decl) int {
	switch d := decl.(type) {
	case *ast.GenDecl:
		switch d.Tok {
		case token.IMPORT:
			return 0
		case token.CONST, token.VAR:
			return 1
		case token.TYPE:
			return 2
		}
	case *ast.FuncDecl:
		if d.Recv != nil {
			return 2 // methods rank with the type region, after their receiver
		}
		return 3
	}
	return 3
}

// kindRegion places a new fragment in the same region order declRegion
// uses for existing declarations; methods never reach it (they anchor to
// their receiver group instead).
func kindRegion(kind SymbolKind) int {
	switch kind {
	case KindConst, KindVar:
		return 1
	case KindType:
		return 2
	default:
		return 3
	}
}

// hasReceiverAnchor reports whether the file declares recv's type or any of
// its methods — the anchor a new method is placed after; without one the
// method falls to the plain-func region at the bottom.
func hasReceiverAnchor(astFile *ast.File, recv string) bool {
	for _, decl := range astFile.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok == token.TYPE && declaresType(d, recv) {
				return true
			}
		case *ast.FuncDecl:
			if d.Recv != nil && workspace.RecvTypeName(d.Recv) == recv {
				return true
			}
		}
	}
	return false
}

// declaresType reports whether the type declaration declares name, grouped
// or not.
func declaresType(gen *ast.GenDecl, name string) bool {
	for _, spec := range gen.Specs {
		if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == name {
			return true
		}
	}
	return false
}

// findMergeableGroup returns the file's existing, non-position-dependent
// grouped (parenthesized) const/var declaration of the given token, if
// there is one — the merge target for a new plain const/var, keeping at
// most one such group per file rather than growing a new one each time.
// A group with any position-dependent member is never a merge target,
// and neither is a standalone (unparenthesized) declaration — converting
// one into a group is a structural rewrite of code nobody asked to
// touch, not a merge.
func findMergeableGroup(astFile *ast.File, tok token.Token) *ast.GenDecl {
	for _, decl := range astFile.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != tok || !gen.Lparen.IsValid() {
			continue
		}
		if groupPositionDependent(gen) {
			continue
		}
		return gen
	}
	return nil
}
