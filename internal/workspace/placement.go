package workspace

import (
	"go/ast"
	"go/token"
)

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

// kindRegion places a new declaration in the same region order declRegion
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

// declPrecedes reports whether decl belongs at or before a new
// declaration's canonical region. Regions rank imports < values <
// types-with-their-methods < funcs; a method anchors only to its own
// receiver group.
func declPrecedes(decl ast.Decl, kind SymbolKind, recv string) bool {
	if kind == KindMethod {
		switch d := decl.(type) {
		case *ast.GenDecl:
			return d.Tok == token.IMPORT || d.Tok == token.CONST || d.Tok == token.VAR ||
				(d.Tok == token.TYPE && declaresType(d, recv))
		case *ast.FuncDecl:
			return d.Recv != nil && RecvTypeName(d.Recv) == recv
		}
		return false
	}
	return declRegion(decl) <= kindRegion(kind)
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
			if d.Recv != nil && RecvTypeName(d.Recv) == recv {
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

// groupPositionDependent reports whether any member of a const group is
// position-dependent — the whole-group counterpart to
// constPositionDependent (which answers the question for one member),
// used to decide whether an existing group is safe to merge new members
// into: a group with any position-dependent member never is.
func groupPositionDependent(gen *ast.GenDecl) bool {
	if gen.Tok != token.CONST {
		return false
	}
	for _, spec := range gen.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Values) == 0 {
			return true
		}
	}
	return GroupUsesIota(gen)
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

// resolveFile finds path within pkg's production or external-test half.
func (w *Workspace) resolveFile(pkg PackagePath, path FilePath) (*File, *Package, bool) {
	unit, ok := w.Unit(pkg)
	if !ok {
		return nil, nil, false
	}
	for _, p := range unit.Members() {
		if file, ok := p.File(path); ok {
			return file, p, true
		}
	}
	return nil, nil, false
}

// InsertOffset returns the canonical insertion offset within pkg's
// fileName for a new declaration of kind/recv, per the placement policy:
// const/var at the top after imports, types after values, funcs at the
// bottom, methods right after their receiver group. A method whose
// receiver group isn't in this file falls to the bottom. Aggregate-owned
// placement analysis, resolved fresh from pkg/fileName here rather than
// accepted as a *File pointer a caller might already be holding.
func (w *Workspace) InsertOffset(pkg PackagePath, fileName FilePath, kind SymbolKind, recv string) (ByteOffset, bool) {
	file, owner, ok := w.resolveFile(pkg, fileName)
	if !ok {
		return 0, false
	}
	effKind, effRecv := kind, recv
	if kind == KindMethod && !hasReceiverAnchor(file.Ast(), recv) {
		effKind, effRecv = KindFunc, ""
	}
	var anchor ast.Decl
	for _, decl := range file.Ast().Decls {
		if declPrecedes(decl, effKind, effRecv) {
			anchor = decl
		}
	}
	if anchor == nil {
		if sp, ok := w.offsetSpan(owner, file, file.Ast().Name.Pos(), file.Ast().Name.End()); ok {
			return sp.End, true
		}
		return ByteOffset(len(file.Src())), true
	}
	if sp, ok := w.offsetSpan(owner, file, anchor.Pos(), anchor.End()); ok {
		return sp.End, true
	}
	return ByteOffset(len(file.Src())), true
}

// TypeDeclOffset returns the insertion offset right after typeName's own
// declaration within pkg's fileName, or ok=false when the type isn't
// declared there — the same "cluster with your type" placement
// declPrecedes already gives methods, extended to a typed iota group
// anchored to that type instead of a receiver.
func (w *Workspace) TypeDeclOffset(pkg PackagePath, fileName FilePath, typeName string) (ByteOffset, bool) {
	file, owner, ok := w.resolveFile(pkg, fileName)
	if !ok {
		return 0, false
	}
	for _, decl := range file.Ast().Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE || !declaresType(gen, typeName) {
			continue
		}
		if sp, ok := w.offsetSpan(owner, file, decl.Pos(), decl.End()); ok {
			return sp.End, true
		}
	}
	return 0, false
}

// MergeableGroupInsertOffset returns the insertion offset just before the
// closing paren of pkg's fileName's existing, non-position-dependent
// grouped (parenthesized) const/var declaration of the given token, or
// ok=false when there is no such group to merge a new plain const/var
// into — keeping at most one such group per file rather than growing a
// new one each time.
func (w *Workspace) MergeableGroupInsertOffset(pkg PackagePath, fileName FilePath, tok token.Token) (ByteOffset, bool) {
	file, owner, ok := w.resolveFile(pkg, fileName)
	if !ok {
		return 0, false
	}
	gen := findMergeableGroup(file.Ast(), tok)
	if gen == nil {
		return 0, false
	}
	sp, ok := w.offsetSpan(owner, file, gen.Rparen, gen.Rparen)
	if !ok {
		return 0, false
	}
	return sp.Start, true
}
