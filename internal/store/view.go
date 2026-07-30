package store

import (
	"errors"
	"fmt"
	"go/token"
	"regexp"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// ErrNarrowlyChecked mirrors workspace.ErrNarrowlyChecked at the View
// boundary, so a caller outside workspace's own ACL (internal/tools) can
// detect it without importing workspace directly.
var ErrNarrowlyChecked = errors.New("workspace was narrowly rechecked: SymbolsImplementing needs a full recheck first")

// Symbol resolves a canonical package address and symbol key ("Name" or
// "Recv.Name") to the symbol, checking Prod before XTest before falling
// back to the external dependency cache — Symbol.Owner names which
// specific half it was found in.
func (v *View) Symbol(pkg workspace.PackagePath, key string) (Symbol, bool) {
	sym, owner, ok := v.ws.ResolveSymbol(pkg, key)
	if !ok {
		return Symbol{}, false
	}
	return Symbol{Key: sym.Key(), Kind: sym.Kind.String(), File: sym.File, Owner: owner.ID}, true
}

// Module is the workspace's module path: the prefix of every workspace
// package address. Valid inside Read, where the snapshot is held.
func (v *View) Module() workspace.PackagePath {
	return v.ws.Module()
}

// Methods enumerates the methods declared on typeName in one package.
func (v *View) Methods(pkg workspace.PackageID, typeName string) []Symbol {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return nil
	}
	var out []Symbol
	for _, s := range p.Symbols() {
		if s.Kind == workspace.KindMethod && s.Recv == typeName {
			out = append(out, Symbol{Key: s.Key(), Kind: s.Kind.String(), File: s.File, Owner: p.ID})
		}
	}
	return out
}

// Packages enumerates every package's identity in the workspace, path
// order, Prod before XTest.
func (v *View) Packages() []workspace.PackageID {
	var out []workspace.PackageID
	for _, addr := range v.ws.UnitKeys() {
		unit, _ := v.ws.Unit(addr)
		if prod := unit.Prod(); prod != nil {
			out = append(out, prod.ID)
		}
		if xtest := unit.XTest(); xtest != nil {
			out = append(out, xtest.ID)
		}
	}
	return out
}

// UnitKeys enumerates every unit's address, sorted — one entry per
// directory, unlike Packages which emits Prod and XTest separately.
func (v *View) UnitKeys() []workspace.PackagePath {
	return v.ws.UnitKeys()
}

// SymbolsImplementing scans for named types whose value or pointer method
// set satisfies the given interface symbol, checked with full type
// information — embedding and promoted methods included. Returns
// ErrNarrowlyChecked if the current generation mixes packages from two
// different type-checking sessions (Recheck v2) — the one analysis that
// can't safely answer without a full recheck first.
func (v *View) SymbolsImplementing(pkg workspace.PackagePath, key string) ([]Symbol, error) {
	matches, err := v.ws.SymbolsImplementing(v.ctx, pkg, key)
	if errors.Is(err, workspace.ErrNarrowlyChecked) {
		return nil, ErrNarrowlyChecked
	}
	if err != nil {
		return nil, err
	}
	return toSymbols(matches), nil
}

// SymbolsLike scans for symbols whose key contains substr, case-insensitively.
func (v *View) SymbolsLike(substr string) []Symbol {
	return toSymbols(v.ws.SymbolsLike(v.ctx, substr))
}

// SymbolsReferencing scans every package's resolved identifier uses for
// references to the given symbol and reports the enclosing declarations, in
// the same address space as every other scanner. The definition itself and
// self-references (recursion) are excluded. Matching is by qualified name —
// (import path, receiver, name) — which is exact for Go and immune to the
// duplicate type-checked instances that test variants create.
func (v *View) SymbolsReferencing(pkg workspace.PackagePath, key string) ([]Symbol, error) {
	matches, err := v.ws.SymbolsReferencing(v.ctx, pkg, key)
	if err != nil {
		return nil, err
	}
	return toSymbols(matches), nil
}

// SymbolsRegexp scans each symbol's own source and collects the symbols
// whose text matches re — the general-purpose matcher for when neither key
// nor name can identify the target. It searches the in-memory truth, so
// unsaved mutations are visible to it. Text outside keyed declarations
// (package clauses, imports, init bodies, floating comments) is not
// addressable by symbol and therefore not searched.
func (v *View) SymbolsRegexp(re *regexp.Regexp) []Symbol {
	return toSymbols(v.ws.SymbolsRegexp(v.ctx, re))
}

// DeclSource extracts the exact source of the symbol's whole top-level
// declaration, doc comment included. For a symbol inside a grouped decl
// this is the entire group; see SpecSource for the narrow slice.
func (v *View) DeclSource(pkg workspace.PackagePath, key string) (string, bool) {
	return v.ws.DeclSource(pkg, key)
}

// SpecSource extracts the exact source of the symbol's own spec, doc
// comment included — the narrowest source for a symbol in a grouped decl,
// rendered as written inside the group (without the group's keyword).
// Falls back to DeclSource when the symbol has no spec.
func (v *View) SpecSource(pkg workspace.PackagePath, key string) (string, bool) {
	return v.ws.SpecSource(pkg, key)
}

// Signature extracts a func or method header without doc comment or body.
// Comma-ok is false for every other symbol kind; compose SpecSource there.
func (v *View) Signature(pkg workspace.PackagePath, key string) (string, bool) {
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
func (v *View) resolvePkg(pkg workspace.PackageID) (*workspace.Package, bool) {
	if p, ok := v.ws.ResolvePackage(pkg); ok {
		return p, true
	}
	if p, ok := v.ws.LookupExternal(pkg.Base()); ok {
		return p, true
	}
	return nil, false
}

// HasPackage reports whether pkg names a package in the workspace —
// never the external cache, see HasExternalPackage.
func (v *View) HasPackage(pkg workspace.PackageID) bool {
	_, ok := v.ws.ResolvePackage(pkg)
	return ok
}

// HasExternalPackage reports whether pkg is resident in the external
// dependency cache.
func (v *View) HasExternalPackage(pkg workspace.PackagePath) bool {
	_, ok := v.ws.LookupExternal(pkg)
	return ok
}

// PackageDoc is pkg's own package-level doc comment.
func (v *View) PackageDoc(pkg workspace.PackageID) (string, bool) {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return "", false
	}
	return p.Doc(), true
}

// PackageFiles is pkg's files, path-sorted.
func (v *View) PackageFiles(pkg workspace.PackageID) ([]workspace.FilePath, bool) {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return nil, false
	}
	files := p.Files()
	out := make([]workspace.FilePath, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out, true
}

// PackageSymbols is pkg's own top-level symbols, key-sorted.
func (v *View) PackageSymbols(pkg workspace.PackageID) ([]Symbol, bool) {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return nil, false
	}
	syms := p.Symbols()
	out := make([]Symbol, len(syms))
	for i, s := range syms {
		out[i] = Symbol{Key: s.Key(), Kind: s.Kind.String(), File: s.File, Owner: p.ID}
	}
	return out, true
}

// ResolveType resolves pkg+key the same way Symbol does, refusing (with
// a ready-to-use error) unless the result is a type — the one gate-check
// search_implementors needs before SymbolsImplementing.
func (v *View) ResolveType(pkg workspace.PackagePath, key string) (workspace.PackageID, error) {
	sym, owner, ok := v.ws.ResolveSymbol(pkg, key)
	if !ok {
		if _, ok := v.ws.LookupExternal(pkg); ok {
			return workspace.PackageID{}, fmt.Errorf("%q is a dependency: its API is served read-only by list_* and describe_*; semantic search stays in the workspace", pkg)
		}
		return workspace.PackageID{}, fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, pkg)
	}
	if sym.Kind != workspace.KindType {
		return workspace.PackageID{}, fmt.Errorf("%q is a %s, not a %s: use the matching describe_* tool", key, sym.Kind, workspace.KindType)
	}
	return owner.ID, nil
}

// ResolveFile resolves a bare filename against pkg's own files.
func (v *View) ResolveFile(pkg workspace.PackageID, fileName string) (workspace.FilePath, error) {
	p, ok := v.resolvePkg(pkg)
	if !ok {
		return "", fmt.Errorf("no package at %q: call list_packages for valid addresses", pkg)
	}
	fp, err := workspace.NewFilePath(v.ws.Module(), p.ID.Base(), fileName)
	if err != nil {
		return "", err
	}
	if _, ok := p.File(fp); !ok {
		return "", fmt.Errorf("no file %q in package %q: call list_files for valid names", fp.Base(), pkg)
	}
	return fp, nil
}

// FileDoc is path's own package-doc comment.
func (v *View) FileDoc(path workspace.FilePath) (string, bool) {
	file, _, ok := v.ws.ResolveFileByPath(path)
	if !ok {
		return "", false
	}
	return file.Doc(), true
}

// ExternalPackageID resolves pkg's full identity in the external
// dependency cache — the counterpart to HasExternalPackage for callers
// that need more than an existence check, since a dependency address
// has no module-relative spelling to reparse a Kind out of.
func (v *View) ExternalPackageID(pkg workspace.PackagePath) (workspace.PackageID, bool) {
	p, ok := v.ws.LookupExternal(pkg)
	if !ok {
		return workspace.PackageID{}, false
	}
	return p.ID, true
}

// Symbol is store's read-only view of one addressable top-level
// declaration: the key, its kind (pre-rendered to a string — nothing in
// internal/tools ever needs to compare kinds as a type, only print or
// gate-check them, and the gate-check has its own narrow method,
// ResolveType), its owning file, and Owner — the specific package
// variant (Prod or XTest) it was found in. Owner is what used to need a
// separate Match type to carry: a scan hit's owning package can be the
// XTest half, which File's canonical-only derivation can't distinguish
// on its own.
type Symbol struct {
	Key   string
	Kind  string
	File  workspace.FilePath
	Owner workspace.PackageID
}

// IsType reports whether s is a type declaration — the one place
// internal/tools ever needs to branch on kind, so it lives as a
// predicate rather than a comparable constant crossing the boundary.
func (s Symbol) IsType() bool {
	return s.Kind == workspace.KindType.String()
}

// toSymbols converts scan hits into store's own Symbol shape — field
// copies plus one Kind.String() render, never a re-resolve: workspace's
// scanners already hold everything a Symbol needs.
func toSymbols(matches []workspace.SymbolMatch) []Symbol {
	out := make([]Symbol, len(matches))
	for i, m := range matches {
		out[i] = Symbol{Key: m.Key, Kind: m.Kind.String(), File: m.File, Owner: m.Pkg}
	}
	return out
}
