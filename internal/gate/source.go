package gate

import "github.com/pedropaccola/gomcp/internal/address"

// DeclSource extracts the exact source of the symbol's whole top-level
// declaration, doc comment included. For a symbol inside a grouped decl
// this is the entire group; see SpecSource for the narrow slice.
func (v *View) DeclSource(pkg address.PkgPath, key string) (string, bool) {
	return v.ws.DeclSource(pkg, key)
}

// Signature extracts a func or method header without doc comment or body.
// Comma-ok is false for every other symbol kind; compose SpecSource there.
func (v *View) Signature(pkg address.PkgPath, key string) (string, bool) {
	return v.ws.Signature(pkg, key)
}

// SpecSource extracts the exact source of the symbol's own spec, doc
// comment included — the narrowest source for a symbol in a grouped decl,
// rendered as written inside the group (without the group's keyword).
// Falls back to DeclSource when the symbol has no spec.
func (v *View) SpecSource(pkg address.PkgPath, key string) (string, bool) {
	return v.ws.SpecSource(pkg, key)
}
