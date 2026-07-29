package store

import (
	"errors"
	"fmt"
	"go/token"
	"regexp"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// ErrNarrowlyChecked mirrors workspace.ErrNarrowlyChecked at the View
// boundary, so a caller outside workspace's own ACL (internal/tools) can
// detect it without importing workspace directly.
var ErrNarrowlyChecked = errors.New("workspace was narrowly rechecked: SymbolsImplementing needs a full recheck first")

// Symbol resolves a package address and symbol key ("Name" or "Recv.Name")
// to the symbol and its owning package's address, checking Prod before
// XTest before falling back to the external dependency cache.
func (v *View) Symbol(pkg address.PkgPath, key string) (Symbol, address.PkgPath, bool) {
	sym, owner, ok := v.ws.ResolveSymbol(pkg, key)
	if !ok {
		return Symbol{}, "", false
	}
	return Symbol{Key: sym.Key(), Kind: sym.Kind.String(), File: sym.File}, owner.PkgPath, true
}

// Module is the workspace's module path: the prefix of every workspace
// package address. Valid inside Read, where the snapshot is held.
func (v *View) Module() address.PkgPath {
	return v.ws.Module()
}

// Methods enumerates the methods declared on typeName in one package.
func (v *View) Methods(pkg address.PkgPath, typeName string) []Symbol {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return nil
	}
	var out []Symbol
	for _, s := range p.Symbols() {
		if s.Kind == workspace.KindMethod && s.Recv == typeName {
			out = append(out, Symbol{Key: s.Key(), Kind: s.Kind.String(), File: s.File})
		}
	}
	return out
}

// Packages enumerates every package's address in the workspace, path
// order, Prod before XTest.
func (v *View) Packages() []address.PkgPath {
	var out []address.PkgPath
	for _, addr := range v.ws.UnitKeys() {
		unit, _ := v.ws.Unit(addr)
		if prod := unit.Prod(); prod != nil {
			out = append(out, prod.PkgPath)
		}
		if xtest := unit.XTest(); xtest != nil {
			out = append(out, xtest.PkgPath)
		}
	}
	return out
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
func (v *View) SymbolsImplementing(pkg address.PkgPath, key string) ([]Match, error) {
	matches, err := v.ws.SymbolsImplementing(v.ctx, pkg, key)
	if errors.Is(err, workspace.ErrNarrowlyChecked) {
		return nil, ErrNarrowlyChecked
	}
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// SymbolsLike scans for symbols whose key contains substr, case-insensitively.
func (v *View) SymbolsLike(substr string) []Match {
	return v.ws.SymbolsLike(v.ctx, substr)
}

// SymbolsReferencing scans every package's resolved identifier uses for
// references to the given symbol and reports the enclosing declarations, in
// the same address space as every other scanner. The definition itself and
// self-references (recursion) are excluded. Matching is by qualified name —
// (import path, receiver, name) — which is exact for Go and immune to the
// duplicate type-checked instances that test variants create.
func (v *View) SymbolsReferencing(pkg address.PkgPath, key string) ([]Match, error) {
	return v.ws.SymbolsReferencing(v.ctx, pkg, key)
}

// SymbolsRegexp scans each symbol's own source and collects the symbols
// whose text matches re — the general-purpose matcher for when neither key
// nor name can identify the target. It searches the in-memory truth, so
// unsaved mutations are visible to it. Text outside keyed declarations
// (package clauses, imports, init bodies, floating comments) is not
// addressable by symbol and therefore not searched.
func (v *View) SymbolsRegexp(re *regexp.Regexp) []Match {
	return v.ws.SymbolsRegexp(v.ctx, re)
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

// resolvePkg resolves pkg to its owning *workspace.Package — the
// workspace, then the external cache — the one private door every
// package-fact method below composes on.
func (v *View) resolvePkg(pkg address.PkgPath) (*workspace.Package, bool) {
	if _, p, _, ok := v.ws.ResolvePackage(pkg); ok {
		return p, true
	}
	if p, ok := v.ws.LookupExternal(pkg); ok {
		return p, true
	}
	return nil, false
}

// HasPackage reports whether pkg names a package in the workspace —
// never the external cache, see HasExternalPackage.
func (v *View) HasPackage(pkg address.PkgPath) bool {
	_, _, _, ok := v.ws.ResolvePackage(pkg)
	return ok
}

// HasExternalPackage reports whether pkg is resident in the external
// dependency cache.
func (v *View) HasExternalPackage(pkg address.PkgPath) bool {
	_, ok := v.ws.LookupExternal(pkg)
	return ok
}

// PackageDoc is pkg's own package-level doc comment.
func (v *View) PackageDoc(pkg address.PkgPath) (string, bool) {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return "", false
	}
	return p.Doc(), true
}

// PackageFiles is pkg's files, path-sorted.
func (v *View) PackageFiles(pkg address.PkgPath) ([]address.FilePath, bool) {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return nil, false
	}
	files := p.Files()
	out := make([]address.FilePath, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out, true
}

// PackageSymbols is pkg's own top-level symbols, key-sorted.
func (v *View) PackageSymbols(pkg address.PkgPath) ([]Symbol, bool) {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return nil, false
	}
	syms := p.Symbols()
	out := make([]Symbol, len(syms))
	for i, s := range syms {
		out[i] = Symbol{Key: s.Key(), Kind: s.Kind.String(), File: s.File}
	}
	return out, true
}

// ResolveType resolves pkg+key the same way resolveAnySymbol does,
// refusing (with a ready-to-use error) unless the result is a type —
// the one gate-check search_implementors needs before SymbolsImplementing.
func (v *View) ResolveType(pkg address.PkgPath, key string) (address.PkgPath, error) {
	sym, owner, ok := v.ws.ResolveSymbol(pkg, key)
	if !ok {
		if _, ok := v.ws.LookupExternal(pkg); ok {
			return "", fmt.Errorf("%q is a dependency: its API is served read-only by list_* and describe_*; semantic search stays in the workspace", pkg)
		}
		return "", fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, pkg)
	}
	if sym.Kind != workspace.KindType {
		return "", fmt.Errorf("%q is a %s, not a %s: use the matching describe_* tool", key, sym.Kind, workspace.KindType)
	}
	return owner.PkgPath, nil
}

// ResolveFile resolves a bare filename against pkg's own files.
func (v *View) ResolveFile(pkg address.PkgPath, fileName string) (address.FilePath, error) {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return "", fmt.Errorf("no package at %q: call list_packages for valid addresses", pkg)
	}
	fp, err := address.NewFilePath(v.ws.Module(), p.PkgPath.Canon(), fileName)
	if err != nil {
		return "", err
	}
	if _, ok := p.File(fp); !ok {
		return "", fmt.Errorf("no file %q in package %q: call list_files for valid names", fp.Base(), pkg)
	}
	return fp, nil
}

// FileDoc is path's own package-doc comment.
func (v *View) FileDoc(path address.FilePath) (string, bool) {
	file, _, ok := v.ws.ResolveFileByPath(path)
	if !ok {
		return "", false
	}
	return file.Doc(), true
}

// Symbol is store's read-only view of one addressable top-level
// declaration: the key, its kind (pre-rendered to a string — nothing in
// internal/tools ever needs to compare kinds as a type, only print or
// gate-check them, and the gate-check has its own narrow method,
// ResolveType), and its owning file.
type Symbol struct {
	Key  string
	Kind string
	File address.FilePath
}

// IsType reports whether s is a type declaration — the one place
// internal/tools ever needs to branch on kind, so it lives as a
// predicate rather than a comparable constant crossing the boundary.
func (s Symbol) IsType() bool {
	return s.Kind == workspace.KindType.String()
}

// Match is one scan hit — a deliberate, temporary alias to
// workspace.SymbolMatch, the one exception to this package's own rule of
// not aliasing workspace types. Removing it requires real package
// ownership identity (a match's Pkg can be XTest-suffixed today, which
// nothing but a resolved *workspace.Package can tell you) — tracked as
// an explicit task in NOTES-address-identity.md's PackageID migration,
// not left to be rediscovered.
type Match = workspace.SymbolMatch
