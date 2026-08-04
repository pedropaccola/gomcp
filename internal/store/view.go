package store

import (
	"cmp"
	"errors"
	"fmt"
	"go/token"
	"regexp"
	"slices"

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
	return Symbol{Key: sym.Key(), Kind: sym.Kind.String(), File: sym.File, Owner: owner.ID, Ignored: sym.Ignored, Directives: slices.Clone(sym.Directives)}, true
}

// Module is the workspace's module path: the prefix of every workspace
// package address. Valid inside Read, where the snapshot is held.
func (v *View) Module() workspace.PackagePath {
	return v.ws.Module()
}

// Methods enumerates the methods declared on typeName across every one
// of pkg's members (Prod, XTest, Ignored).
func (v *View) Methods(pkg workspace.PackagePath, typeName string) []Symbol {
	members, ok := v.resolveMembers(pkg)
	if !ok {
		return nil
	}
	var out []Symbol
	for _, p := range members {
		for _, s := range p.Symbols() {
			if s.Kind == workspace.KindMethod && s.Recv == typeName {
				out = append(out, Symbol{Key: s.Key(), Kind: s.Kind.String(), File: s.File, Owner: p.ID})
			}
		}
	}
	return out
}

// Packages enumerates every package's identity in the workspace, path
// order, Prod before XTest.
func (v *View) Packages() []workspace.PackageID {
	var out []workspace.PackageID
	for _, addr := range v.ws.MemberKeys() {
		for _, pkg := range v.ws.MembersOf(addr) {
			out = append(out, pkg.ID)
		}
	}
	return out
}

// MemberKeys enumerates every unit's address, sorted — one entry per
// directory, unlike Packages which emits Prod and XTest separately.
func (v *View) MemberKeys() []workspace.PackagePath {
	return v.ws.MemberKeys()
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
// fileName, when non-empty, scopes resolution exactly to that file
// rather than a primary-preference guess.
func (v *View) DeclSource(pkg workspace.PackagePath, key, fileName string) (string, bool) {
	return v.ws.DeclSource(pkg, key, fileName)
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

// resolveMembers resolves pkg to every one of its member packages (Prod,
// XTest, Ignored, Prod-before-XTest-before-Ignored order) — the workspace
// first, then the external cache as a last resort — the one private
// door every package-fact method below composes on.
func (v *View) resolveMembers(pkg workspace.PackagePath) ([]*workspace.Package, bool) {
	if members := v.ws.MembersOf(pkg); len(members) > 0 {
		return members, true
	}
	if p, ok := v.ws.LookupExternal(pkg); ok {
		return []*workspace.Package{p}, true
	}
	return nil, false
}

// HasPackage reports whether pkg names a package in the workspace —
// never the external cache, see HasExternalPackage.
func (v *View) HasPackage(pkg workspace.PackagePath) bool {
	return len(v.ws.MembersOf(pkg)) > 0
}

// HasExternalPackage reports whether pkg is resident in the external
// dependency cache.
func (v *View) HasExternalPackage(pkg workspace.PackagePath) bool {
	_, ok := v.ws.LookupExternal(pkg)
	return ok
}

// PackageDoc is pkg's own package-level doc comment — its Prod half's,
// specifically, since XTest/Ignored halves don't carry the package's own
// canonical doc.
func (v *View) PackageDoc(pkg workspace.PackagePath) (string, bool) {
	members, ok := v.resolveMembers(pkg)
	if !ok {
		return "", false
	}
	return members[0].Doc(), true
}

// PackageFiles is pkg's files across every one of its members (Prod,
// XTest), path-sorted — an agent addresses the one canonical package,
// never a specific internal kind, and a file's own basename is already
// unique per directory regardless of which kind loaded it.
func (v *View) PackageFiles(pkg workspace.PackagePath) ([]workspace.FilePath, bool) {
	members, ok := v.resolveMembers(pkg)
	if !ok {
		return nil, false
	}
	var out []workspace.FilePath
	for _, p := range members {
		for _, f := range p.Files() {
			out = append(out, f.Path)
		}
	}
	slices.Sort(out)
	return out, true
}

// PackageSymbols is pkg's own top-level symbols across every one of its
// members (Prod, XTest, Ignored), key-sorted.
func (v *View) PackageSymbols(pkg workspace.PackagePath) ([]Symbol, bool) {
	members, ok := v.resolveMembers(pkg)
	if !ok {
		return nil, false
	}
	var out []Symbol
	for _, p := range members {
		for _, s := range p.Symbols() {
			out = append(out, Symbol{Key: s.Key(), Kind: s.Kind.String(), File: s.File, Owner: p.ID})
		}
	}
	slices.SortFunc(out, func(a, b Symbol) int { return cmp.Compare(a.Key, b.Key) })
	return out, true
}

// ResolveType resolves addr+key the same way Symbol/SymbolIn does,
// refusing (with a ready-to-use error) unless the result is a type —
// the one gate-check the implementor-search flow needs before
// SymbolsImplementing. fileName, when non-empty, scopes resolution
// exactly to that file rather than a primary-preference guess.
func (v *View) ResolveType(addr, key, fileName string) (workspace.PackageID, error) {
	pkg, err := workspace.NewPackagePath(v.Module(), addr)
	if err != nil {
		return workspace.PackageID{}, err
	}
	var sym Symbol
	var ok bool
	if fileName != "" {
		sym, ok = v.SymbolIn(pkg, key, fileName)
	} else {
		sym, ok = v.Symbol(pkg, key)
	}
	if !ok {
		if v.HasExternalPackage(workspace.PackagePath(addr)) {
			return workspace.PackageID{}, fmt.Errorf("%q is a dependency: read-only, semantic search stays scoped to the workspace", addr)
		}
		return workspace.PackageID{}, workspace.NoSymbolError(key, addr)
	}
	if sym.Kind != workspace.KindType.String() {
		return workspace.PackageID{}, fmt.Errorf("%q is a %s, not a %s", key, sym.Kind, workspace.KindType)
	}
	return sym.Owner, nil
}

// ResolveFile resolves a bare filename against pkg's own files, checking
// every one of its members (Prod, XTest, Ignored) — a file's basename is
// already unique per directory, so at most one member can ever hold it.
// Returns the file's owner alongside its resolved path, so a caller
// holding only the bare canonical pkg can still tell which kind actually
// owns it.
func (v *View) ResolveFile(pkg workspace.PackagePath, fileName string) (workspace.FilePath, workspace.PackageID, error) {
	members, ok := v.resolveMembers(pkg)
	if !ok {
		return "", workspace.PackageID{}, workspace.NoPackageError(pkg)
	}
	fp, err := workspace.NewFilePath(v.ws.Module(), pkg, fileName)
	if err != nil {
		return "", workspace.PackageID{}, err
	}
	for _, p := range members {
		if _, ok := p.File(fp); ok {
			return fp, p.ID, nil
		}
	}
	return "", workspace.PackageID{}, errNoFile(fp.Base(), pkg)
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

// FileDirectives is path's own leading compiler directives.
func (v *View) FileDirectives(path workspace.FilePath) ([]string, bool) {
	file, _, ok := v.ws.ResolveFileByPath(path)
	if !ok {
		return nil, false
	}
	return slices.Clone(file.Directives), true
}

// SymbolIn resolves a canonical package address, symbol key, and file
// name to the symbol — fileName is an assertion, not a hint: only a
// declaration whose own file matches exactly is returned, searched
// across every member (Prod and XTest alike), never falling back to a
// primary-preference guess when it doesn't match anything. The only way
// to reach a declaration a same-named sibling elsewhere would otherwise
// shadow.
func (v *View) SymbolIn(pkg workspace.PackagePath, key, fileName string) (Symbol, bool) {
	sym, owner, ok := v.ws.ResolveSymbolIn(pkg, key, fileName)
	if !ok {
		return Symbol{}, false
	}
	return Symbol{Key: sym.Key(), Kind: sym.Kind.String(), File: sym.File, Owner: owner.ID, Ignored: sym.Ignored, Directives: slices.Clone(sym.Directives)}, true
}

// FileIgnored reports whether path's own build constraint excludes it
// from the current build.
func (v *View) FileIgnored(path workspace.FilePath) (bool, bool) {
	file, _, ok := v.ws.ResolveFileByPath(path)
	if !ok {
		return false, false
	}
	return file.Ignored, true
}

// FileGenerated reports whether path carries a generated-file marker
// among its leading comment/blank lines — checked fresh against its
// current bytes every call, never cached.
func (v *View) FileGenerated(path workspace.FilePath) (bool, bool) {
	file, _, ok := v.ws.ResolveFileByPath(path)
	if !ok {
		return false, false
	}
	return workspace.IsGenerated(file.Src()), true
}

// FileKind is path's own owning package's shape (Prod or XTest) —
// omitted (empty) for the common Prod case, present only for XTest.
func (v *View) FileKind(path workspace.FilePath) (string, bool) {
	_, owner, ok := v.ws.ResolveFileByPath(path)
	if !ok {
		return "", false
	}
	if owner.ID.Kind() == workspace.KindProd {
		return "", true
	}
	return owner.ID.Kind().String(), true
}

// Symbol is store's read-only view of one addressable top-level
// declaration: the key, its kind (pre-rendered to a string — nothing in
// internal/tools ever needs to compare kinds as a type, only print or
// compare it), the file it lives in, which package owns it (Prod or
// XTest, distinguished by PackageID.Kind), and whether its own file is
// excluded from the current build.
type Symbol struct {
	Key        string
	Kind       string
	File       workspace.FilePath
	Owner      workspace.PackageID
	Ignored    bool
	Directives []string
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
