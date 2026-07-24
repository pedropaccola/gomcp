package engine

import (
	"bytes"
	"go/ast"
	"go/token"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// declSource extracts the exact bytes of the symbol's whole top-level
// declaration, doc comment included. For a symbol inside a grouped decl this
// is the entire group; see specSource for the narrow slice.
func (v *View) declSource(sym *workspace.Symbol) ([]byte, bool) {
	start := sym.Decl().Pos()
	if doc := workspace.DocOf(sym.Decl()); doc != nil {
		start = doc.Pos()
	}
	return v.sliceSrc(sym.File, start, sym.Decl().End())
}

// specSource extracts the exact bytes of the symbol's own spec, doc comment
// included — the narrowest source for a symbol in a grouped decl, rendered as
// written inside the group (without the group's keyword). Falls back to
// declSource when the symbol has no spec.
func (v *View) specSource(sym *workspace.Symbol) ([]byte, bool) {
	if sym.Spec() == nil {
		return v.declSource(sym)
	}
	start := sym.Spec().Pos()
	if doc := workspace.DocOf(sym.Spec()); doc != nil {
		start = doc.Pos()
	}
	return v.sliceSrc(sym.File, start, sym.Spec().End())
}

// signature extracts a func or method header without doc comment or body.
// Comma-ok is false for every other symbol kind; compose specSource there.
func (v *View) signature(sym *workspace.Symbol) ([]byte, bool) {
	fn, ok := sym.Decl().(*ast.FuncDecl)
	if !ok {
		return nil, false
	}
	end := fn.End()
	if fn.Body != nil {
		end = fn.Body.Lbrace
	}
	src, ok := v.sliceSrc(sym.File, fn.Pos(), end)
	if !ok {
		return nil, false
	}
	return bytes.TrimRight(src, " \t\n"), true
}

// sliceSrc cuts the exact original bytes [from, to) out of a tracked file's
// canonical source.
func (v *View) sliceSrc(path address.RelativePath, from, to token.Pos) ([]byte, bool) {
	file, _, ok := v.resolveFile(path)
	if !ok {
		return nil, false
	}
	sp, ok := v.offsetSpan(path, from, to)
	if !ok {
		return nil, false
	}
	return file.Src()[sp.start:sp.end], true
}

// DeclSource extracts the exact source of the symbol's whole top-level
// declaration, doc comment included. For a symbol inside a grouped decl
// this is the entire group; see SpecSource for the narrow slice.
func (v *View) DeclSource(pkg address.PkgPath, key string) (string, bool) {
	sym, _, ok := v.resolveSymbol(pkg, key)
	if !ok {
		return "", false
	}
	b, ok := v.declSource(sym)
	return string(b), ok
}

// SpecSource extracts the exact source of the symbol's own spec, doc
// comment included — the narrowest source for a symbol in a grouped decl,
// rendered as written inside the group (without the group's keyword).
// Falls back to DeclSource when the symbol has no spec.
func (v *View) SpecSource(pkg address.PkgPath, key string) (string, bool) {
	sym, _, ok := v.resolveSymbol(pkg, key)
	if !ok {
		return "", false
	}
	b, ok := v.specSource(sym)
	return string(b), ok
}

// Signature extracts a func or method header without doc comment or body.
// Comma-ok is false for every other symbol kind; compose SpecSource there.
func (v *View) Signature(pkg address.PkgPath, key string) (string, bool) {
	sym, _, ok := v.resolveSymbol(pkg, key)
	if !ok {
		return "", false
	}
	b, ok := v.signature(sym)
	return string(b), ok
}
