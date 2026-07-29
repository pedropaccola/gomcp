package workspace

import (
	"cmp"
	"context"
	"fmt"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
)

// SymbolMatch identifies one scan hit by address — a package and a symbol
// key, not a pointer, safe to return across the Aggregate boundary.
// Callers needing more resolve it fresh from the address.
type SymbolMatch struct {
	Pkg address.PkgPath
	Key string
}

// symbolsWhere scans every symbol in the workspace and collects those for
// which pred holds — the one primitive under every other scanner; new
// filters should compose on it as predicates. Checks ctx once per unit
// and stops early, returning whatever was found so far, if it's been
// canceled or its deadline has passed. Iterates by unit address directly
// (not allPackages) so each match records the canonical address callers
// can resolve back through — an XTest package's own PkgPath field is not
// necessarily that address (go/packages gives external-test variants
// their own augmented import path).
func (w *Workspace) symbolsWhere(ctx context.Context, pred func(*Package, *Symbol) bool) []SymbolMatch {
	var out []SymbolMatch
	for _, addr := range w.UnitKeys() {
		if ctx.Err() != nil {
			return out
		}
		unit, _ := w.Unit(addr)
		for _, pkg := range unit.Members() {
			for _, sym := range pkg.Symbols() {
				if pred(pkg, sym) {
					out = append(out, SymbolMatch{Pkg: addr, Key: sym.Key()})
				}
			}
		}
	}
	return out
}

// SymbolsLike scans for symbols whose key contains substr, case-insensitively.
func (w *Workspace) SymbolsLike(ctx context.Context, substr string) []SymbolMatch {
	needle := strings.ToLower(substr)
	return w.symbolsWhere(ctx, func(_ *Package, sym *Symbol) bool {
		return strings.Contains(strings.ToLower(sym.Key()), needle)
	})
}

// scanSource picks the slice a text scan should see: the narrow spec for
// a symbol inside a grouped decl (so a hit attributes to one symbol, not
// the whole group), and the full declaration — keyword and doc included
// — everywhere else.
func (w *Workspace) scanSource(pkg *Package, sym *Symbol) ([]byte, bool) {
	var sp span
	var ok bool
	if _, grouped := sym.GroupOf(); grouped {
		sp, ok = w.specSpan(pkg, sym)
	} else {
		sp, ok = w.declSpan(pkg, sym)
	}
	if !ok {
		return nil, false
	}
	file, _ := pkg.File(sym.File)
	return file.Src()[sp.start:sp.end], true
}

// SymbolsRegexp scans each symbol's own source and collects the symbols
// whose text matches re — the general-purpose matcher for when neither
// key nor name can identify the target. It searches the in-memory truth,
// so unsaved mutations are visible to it. Text outside keyed declarations
// (package clauses, imports, init bodies, floating comments) is not
// addressable by symbol and therefore not searched.
func (w *Workspace) SymbolsRegexp(ctx context.Context, re *regexp.Regexp) []SymbolMatch {
	return w.symbolsWhere(ctx, func(pkg *Package, sym *Symbol) bool {
		src, ok := w.scanSource(pkg, sym)
		return ok && re.Match(src)
	})
}

// SymbolsImplementing scans for named types whose value or pointer method
// set satisfies the given interface symbol, checked with full type
// information — embedding and promoted methods included. Refuses with
// ErrNarrowlyChecked if the current generation was assembled by a
// dirty-scoped recheck: types.Implements needs every compared type built
// from one consistent type-checking session, which a narrow recheck's
// carried-forward packages don't guarantee — see ErrNarrowlyChecked.
func (w *Workspace) SymbolsImplementing(ctx context.Context, pkg address.PkgPath, key string) ([]SymbolMatch, error) {
	if w.narrowlyChecked {
		return nil, ErrNarrowlyChecked
	}
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return nil, fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	obj := owner.objectOf(sym)
	if obj == nil {
		return nil, fmt.Errorf("type information unavailable for %q", sym.Key())
	}
	iface, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("%q is not an interface type", sym.Key())
	}
	if iface.Empty() {
		return nil, fmt.Errorf("%q is an empty interface: every type implements it", sym.Key())
	}
	matches := w.symbolsWhere(ctx, func(candOwner *Package, cand *Symbol) bool {
		if cand.Kind != KindType || cand == sym {
			return false
		}
		candObj := candOwner.objectOf(cand)
		if candObj == nil {
			return false
		}
		t := candObj.Type()
		return types.Implements(t, iface) || types.Implements(types.NewPointer(t), iface)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

// SymbolsReferencing scans every package's resolved identifier uses for
// references to the given symbol and reports the enclosing declarations,
// sorted by package then key. The definition itself and self-references
// (recursion) are excluded. Matching is by qualified name — (import path,
// receiver, name) — which is exact for Go and immune to the duplicate
// type-checked instances that test variants create; also gated to
// package-level declarations and methods only — see isPackageLevelUse's
// doc comment for why that guard matters. Iterates by unit address
// directly (not allPackages), same reason as symbolsWhere: each match
// must record the canonical address, not an XTest package's own
// possibly-augmented PkgPath.
func (w *Workspace) SymbolsReferencing(ctx context.Context, pkg address.PkgPath, key string) ([]SymbolMatch, error) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return nil, fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	target, ok := keyOf(owner.objectOf(sym))
	if !ok {
		return nil, fmt.Errorf("type information unavailable for %q", sym.Key())
	}
	type addrRef struct {
		Addr address.PkgPath
		Pkg  *Package
		Sym  *Symbol
	}
	seen := make(map[*Symbol]bool)
	var refs []addrRef
	for _, addr := range w.UnitKeys() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		unit, _ := w.Unit(addr)
		for _, p := range unit.Members() {
			if p.TypesInfo() == nil {
				continue
			}
			for ident, obj := range p.TypesInfo().Uses {
				if !isPackageLevelUse(obj) {
					continue
				}
				k, ok := keyOf(obj)
				if !ok || k != target {
					continue
				}
				encl, ok := p.symbolAt(ident.Pos())
				if !ok || encl == sym || seen[encl] {
					continue
				}
				seen[encl] = true
				refs = append(refs, addrRef{Addr: addr, Pkg: p, Sym: encl})
			}
		}
	}
	slices.SortFunc(refs, func(a, b addrRef) int {
		if c := cmp.Compare(a.Addr, b.Addr); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Pkg.Name, b.Pkg.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.Sym.Key(), b.Sym.Key())
	})
	out := make([]SymbolMatch, len(refs))
	for i, ref := range refs {
		out[i] = SymbolMatch{Pkg: ref.Addr, Key: ref.Sym.Key()}
	}
	return out, nil
}

// ResolveFileByPath resolves a file path to the file and its owning
// package, checking the production package before the external test
// package, falling back to the external dependency cache. path's own
// directory is its owning package's canonical PkgPath by construction
// (every FilePath is built as pkg+"/"+basename — see address.PkgPath.File)
// — no scan over every package needed.
func (w *Workspace) ResolveFileByPath(path address.FilePath) (*File, *Package, bool) {
	pkgPath := path.Dir()
	if unit, ok := w.Unit(pkgPath); ok {
		for _, pkg := range unit.Members() {
			if file, ok := pkg.File(path); ok {
				return file, pkg, true
			}
		}
	}
	if pkg, ok := w.LookupExternal(pkgPath); ok {
		if file, ok := pkg.File(path); ok {
			return file, pkg, true
		}
	}
	return nil, nil, false
}

// AddressAtLine resolves a file:line coordinate to the address of the
// enclosing top-level declaration — the line-keyed sibling of position-
// based resolution, for diagnostics, which carry a line number rather
// than a token.Pos once translated. In grouped decls it prefers the
// symbol whose own spec's line range contains the line.
func (w *Workspace) AddressAtLine(path address.FilePath, line int) (pkg address.PkgPath, key string, ok bool) {
	_, owner, ok := w.ResolveFileByPath(path)
	if !ok {
		return "", "", false
	}
	fset := w.FsetOf(owner)
	var groupHit *Symbol
	for _, sym := range owner.Symbols() {
		if sym.File != path {
			continue
		}
		start := sym.Decl().Pos()
		if doc := DocOf(sym.Decl()); doc != nil {
			start = doc.Pos()
		}
		from, to := fset.Position(start).Line, fset.Position(sym.Decl().End()).Line
		if line < from || line > to {
			continue
		}
		if sym.Spec() == nil {
			return owner.PkgPath, sym.Key(), true
		}
		specFrom, specTo := fset.Position(sym.Spec().Pos()).Line, fset.Position(sym.Spec().End()).Line
		if line >= specFrom && line <= specTo {
			return owner.PkgPath, sym.Key(), true
		}
		if groupHit == nil {
			groupHit = sym
		}
	}
	if groupHit != nil {
		return owner.PkgPath, groupHit.Key(), true
	}
	return "", "", false
}
