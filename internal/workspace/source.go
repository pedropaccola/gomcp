package workspace

import (
	"go/ast"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
)

// DeclSource extracts the exact source of key's whole top-level
// declaration, doc comment included. For a symbol inside a grouped decl
// this is the entire group; see SpecSource for the narrow slice.
func (w *Workspace) DeclSource(pkg address.PkgPath, key string) (string, bool) {
	sym, owner, ok := w.resolveSymbol(pkg, key)
	if !ok {
		return "", false
	}
	sp, ok := w.declSpan(owner, sym)
	if !ok {
		return "", false
	}
	file, _ := owner.File(sym.File)
	return string(file.Src()[sp.start:sp.end]), true
}

// SpecSource extracts the exact source of key's own spec, doc comment
// included — the narrowest source for a symbol in a grouped decl,
// rendered as written inside the group (without the group's keyword).
// Falls back to DeclSource when the symbol has no spec.
func (w *Workspace) SpecSource(pkg address.PkgPath, key string) (string, bool) {
	sym, owner, ok := w.resolveSymbol(pkg, key)
	if !ok {
		return "", false
	}
	sp, ok := w.specSpan(owner, sym)
	if !ok {
		return "", false
	}
	file, _ := owner.File(sym.File)
	return string(file.Src()[sp.start:sp.end]), true
}

// Signature extracts key's func or method header without doc comment or
// body. Comma-ok is false for every other symbol kind; compose
// SpecSource there.
func (w *Workspace) Signature(pkg address.PkgPath, key string) (string, bool) {
	sym, owner, ok := w.resolveSymbol(pkg, key)
	if !ok {
		return "", false
	}
	fn, ok := sym.Decl().(*ast.FuncDecl)
	if !ok {
		return "", false
	}
	end := fn.End()
	if fn.Body != nil {
		end = fn.Body.Lbrace
	}
	file, ok := owner.File(sym.File)
	if !ok {
		return "", false
	}
	sp, ok := offsetSpan(w, owner, file, fn.Pos(), end)
	if !ok {
		return "", false
	}
	return strings.TrimRight(string(file.Src()[sp.start:sp.end]), " \t\n"), true
}
