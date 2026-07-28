package store

import (
	"errors"
	"fmt"
	"go/token"
	"regexp"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// ErrNarrowlyChecked mirrors workspace.ErrNarrowlyChecked at the View
// boundary, so a caller outside workspace's own ACL (internal/tools) can
// detect it without importing workspace directly.
var ErrNarrowlyChecked = errors.New("workspace was narrowly rechecked: SymbolsImplementing needs a full recheck first")

// Package resolves a canonical package address to its production package.
func (v *View) Package(pkg address.PkgPath) (dto.Package, bool) {
	return v.ws.Package(pkg)
}

// ExternalPackage resolves a dependency resident in the external cache;
// LoadExternal fills the cache outside a Read call.
func (v *View) ExternalPackage(pkg address.PkgPath) (dto.Package, bool) {
	return v.ws.ExternalPackage(pkg)
}

// Symbol resolves a package address and symbol key ("Name" or "Recv.Name")
// to the symbol and its owning package, checking Prod before XTest.
func (v *View) Symbol(pkg address.PkgPath, key string) (dto.Symbol, dto.Package, bool) {
	return v.ws.Symbol(pkg, key)
}

// File resolves a bare filename against an already-resolved package's own
// files.
func (v *View) File(pkg dto.Package, fileName string) (dto.File, error) {
	fp, err := address.NewFilePath(v.Module(), pkg.Path, fileName)
	if err != nil {
		return dto.File{}, err
	}
	if file, ok := pkg.File(fp); ok {
		return file, nil
	}
	return dto.File{}, fmt.Errorf("no file %q in package %q: call list_files for valid names", fp.Base(), pkg.Path.Base())
}

// Module is the workspace's module path: the prefix of every workspace
// package address. Valid inside Read, where the snapshot is held.
func (v *View) Module() address.PkgPath {
	return v.ws.Module()
}

// Methods enumerates the methods declared on typeName in one package.
func (v *View) Methods(pkg dto.Package, typeName string) []dto.Symbol {
	var out []dto.Symbol
	for _, sym := range pkg.Symbols {
		if sym.Kind == dto.KindMethod && sym.Recv == typeName {
			out = append(out, sym)
		}
	}
	return out
}

// Packages enumerates every package in the workspace: addresses in path
// order, Prod before XTest.
func (v *View) Packages() []dto.Package {
	return v.ws.Packages()
}

// UnitKeys enumerates every unit's address, sorted — one entry per
// directory, unlike Packages which emits Prod and XTest separately.
func (v *View) UnitKeys() []address.PkgPath {
	return v.ws.UnitKeys()
}

// SymbolsImplementing scans for named types whose value or pointer method
// set satisfies the given interface symbol, checked with full type
// information — embedding and promoted methods included. Returns
// ErrNarrowlyChecked if the current generation mixes packages from two
// different type-checking sessions (Recheck v2) — the one analysis that
// can't safely answer without a full recheck first.
func (v *View) SymbolsImplementing(pkg address.PkgPath, key string) ([]dto.Match, error) {
	matches, err := v.ws.SymbolsImplementing(v.ctx, pkg, key)
	if errors.Is(err, workspace.ErrNarrowlyChecked) {
		return nil, ErrNarrowlyChecked
	}
	if err != nil {
		return nil, err
	}
	return v.toMatches(matches), nil
}

// SymbolsLike scans for symbols whose key contains substr, case-insensitively.
func (v *View) SymbolsLike(substr string) []dto.Match {
	return v.toMatches(v.ws.SymbolsLike(v.ctx, substr))
}

// SymbolsReferencing scans every package's resolved identifier uses for
// references to the given symbol and reports the enclosing declarations, in
// the same address space as every other scanner. The definition itself and
// self-references (recursion) are excluded. Matching is by qualified name —
// (import path, receiver, name) — which is exact for Go and immune to the
// duplicate type-checked instances that test variants create.
func (v *View) SymbolsReferencing(pkg address.PkgPath, key string) ([]dto.Match, error) {
	matches, err := v.ws.SymbolsReferencing(v.ctx, pkg, key)
	if err != nil {
		return nil, err
	}
	return v.toMatches(matches), nil
}

// SymbolsRegexp scans each symbol's own source and collects the symbols
// whose text matches re — the general-purpose matcher for when neither key
// nor name can identify the target. It searches the in-memory truth, so
// unsaved mutations are visible to it. Text outside keyed declarations
// (package clauses, imports, init bodies, floating comments) is not
// addressable by symbol and therefore not searched.
func (v *View) SymbolsRegexp(re *regexp.Regexp) []dto.Match {
	return v.toMatches(v.ws.SymbolsRegexp(v.ctx, re))
}

// toMatches translates workspace's address-only scan hits into dto's own
// shape.
func (v *View) toMatches(ms []workspace.SymbolMatch) []dto.Match {
	out := make([]dto.Match, 0, len(ms))
	for _, m := range ms {
		sym, pkg, ok := v.Symbol(m.Pkg, m.Key)
		if !ok {
			continue
		}
		out = append(out, dto.Match{Package: pkg, Symbol: sym})
	}
	return out
}

// DeclSource extracts the exact source of the symbol's whole top-level
// declaration, doc comment included. For a symbol inside a grouped decl
// this is the entire group; see SpecSource for the narrow slice.
func (v *View) DeclSource(pkg address.PkgPath, key string) (string, bool) {
	return v.ws.DeclSource(pkg, key)
}

// SpecSource extracts the exact source of the symbol's own spec, doc
// comment included — the narrowest source for a symbol in a grouped decl,
// rendered as written inside the group (without the group's keyword).
// Falls back to DeclSource when the symbol has no spec.
func (v *View) SpecSource(pkg address.PkgPath, key string) (string, bool) {
	return v.ws.SpecSource(pkg, key)
}

// Signature extracts a func or method header without doc comment or body.
// Comma-ok is false for every other symbol kind; compose SpecSource there.
func (v *View) Signature(pkg address.PkgPath, key string) (string, bool) {
	return v.ws.Signature(pkg, key)
}

// fsetOf is the FileSet a package's positions live in: the external
// cache's for dependencies, the workspace FileSet otherwise.
func (v *View) fsetOf(pkg *workspace.Package) *token.FileSet {
	return v.ws.FsetOf(pkg)
}
