package engine

import (
	"cmp"
	"fmt"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// Match is one hit of a workspace-wide scan, engine's own copy of the
// matching package and symbol.
type Match struct {
	Pkg Package
	Sym Symbol
}

// match is one hit of a workspace-wide scan, in the workspace's own model
// types — the internal shape scanners compose on before translating to the
// public Match at the boundary.
type match struct {
	Pkg *workspace.Package
	Sym *workspace.Symbol
}

// symbolsWhere scans every symbol in the workspace (Prod and XTest
// packages) and collects those for which pred holds, in the workspace's
// own model types. It is the primitive under every other scanner; new
// filters should compose on it as predicates. Checks v.ctx once per
// package and stops early, returning whatever was found so far, if it's
// been canceled or its deadline has passed — best-effort, since this
// scanner has no error return to signal cancellation through.
func (v *View) symbolsWhere(pred func(*workspace.Package, *workspace.Symbol) bool) []match {
	var out []match
	for _, pkg := range v.allPackages() {
		if v.ctx.Err() != nil {
			return out
		}
		for _, sym := range pkg.Symbols() {
			if pred(pkg, sym) {
				out = append(out, match{Pkg: pkg, Sym: sym})
			}
		}
	}
	return out
}

// SymbolsLike scans for symbols whose key contains substr, case-insensitively.
func (v *View) SymbolsLike(substr string) []Match {
	needle := strings.ToLower(substr)
	matches := v.symbolsWhere(func(_ *workspace.Package, sym *workspace.Symbol) bool {
		return strings.Contains(strings.ToLower(sym.Key()), needle)
	})
	return newMatches(matches)
}

// SymbolsRegexp scans each symbol's own source and collects the symbols
// whose text matches re — the general-purpose matcher for when neither key
// nor name can identify the target. It searches the in-memory truth, so
// unsaved mutations are visible to it. Text outside keyed declarations
// (package clauses, imports, init bodies, floating comments) is not
// addressable by symbol and therefore not searched.
func (v *View) SymbolsRegexp(re *regexp.Regexp) []Match {
	matches := v.symbolsWhere(func(_ *workspace.Package, sym *workspace.Symbol) bool {
		src, ok := v.scanSource(sym)
		return ok && re.Match(src)
	})
	return newMatches(matches)
}

// scanSource picks the slice a text scan should see: the narrow spec for a
// symbol inside a grouped decl (so a hit attributes to one symbol, not the
// whole group), and the full declaration — keyword and doc included —
// everywhere else.
func (v *View) scanSource(sym *workspace.Symbol) ([]byte, bool) {
	if _, grouped := groupOf(sym); grouped {
		return v.specSource(sym)
	}
	return v.declSource(sym)
}

// SymbolsImplementing scans for named types whose value or pointer method
// set satisfies the given interface symbol, checked with full type
// information — embedding and promoted methods included.
func (v *View) SymbolsImplementing(pkg address.PkgPath, key string) ([]Match, error) {
	sym, _, ok := v.resolveSymbol(pkg, key)
	if !ok {
		return nil, fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	obj := v.objectOf(sym)
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
	matches := v.symbolsWhere(func(_ *workspace.Package, cand *workspace.Symbol) bool {
		if cand.Kind != workspace.KindType || cand == sym {
			return false
		}
		candObj := v.objectOf(cand)
		if candObj == nil {
			return false
		}
		t := candObj.Type()
		return types.Implements(t, iface) || types.Implements(types.NewPointer(t), iface)
	})
	if err := v.ctx.Err(); err != nil {
		return nil, err
	}
	return newMatches(matches), nil
}

// SymbolsReferencing scans every package's resolved identifier uses for
// references to the given symbol and reports the enclosing declarations, in
// the same address space as every other scanner. The definition itself and
// self-references (recursion) are excluded. Matching is by qualified name —
// (import path, receiver, name) — which is exact for Go and immune to the
// duplicate type-checked instances that test variants create.
func (v *View) SymbolsReferencing(pkg address.PkgPath, key string) ([]Match, error) {
	matches, err := v.symbolsReferencing(pkg, key)
	if err != nil {
		return nil, err
	}
	sortMatches(matches)
	return newMatches(matches), nil
}

// symbolFromPos resolves a file position to the symbol of the enclosing
// top-level declaration, in the workspace's own model types — the bridge
// from positional facts (type uses, diagnostics) to the symbol address
// space. In grouped decls it prefers the symbol whose own spec contains
// the position.
func (v *View) symbolFromPos(path address.RelativePath, pos token.Pos) (*workspace.Symbol, *workspace.Package, bool) {
	_, owner, ok := v.resolveFile(path)
	if !ok {
		return nil, nil, false
	}
	var groupHit *workspace.Symbol
	for _, sym := range owner.Symbols() {
		if sym.File != path {
			continue
		}
		start := sym.Decl().Pos()
		if doc := workspace.DocOf(sym.Decl()); doc != nil {
			start = doc.Pos()
		}
		if pos < start || pos >= sym.Decl().End() {
			continue
		}
		if sym.Spec() == nil {
			return sym, owner, true
		}
		if pos >= sym.Spec().Pos() && pos < sym.Spec().End() {
			return sym, owner, true
		}
		if groupHit == nil {
			groupHit = sym
		}
	}
	if groupHit != nil {
		return groupHit, owner, true
	}
	return nil, nil, false
}

// symbolFromLine resolves a file:line coordinate to the enclosing
// top-level declaration's symbol — the line-keyed sibling of symbolFromPos
// for diagnostics, which carry a line number rather than a token.Pos once
// translated. In grouped decls it prefers the symbol whose own spec's line
// range contains the line.
func (v *View) symbolFromLine(path address.RelativePath, line int) (*workspace.Symbol, *workspace.Package, bool) {
	_, owner, ok := v.resolveFile(path)
	if !ok {
		return nil, nil, false
	}
	fset := v.fsetOf(owner)
	var groupHit *workspace.Symbol
	for _, sym := range owner.Symbols() {
		if sym.File != path {
			continue
		}
		start := sym.Decl().Pos()
		if doc := workspace.DocOf(sym.Decl()); doc != nil {
			start = doc.Pos()
		}
		from, to := fset.Position(start).Line, fset.Position(sym.Decl().End()).Line
		if line < from || line > to {
			continue
		}
		if sym.Spec() == nil {
			return sym, owner, true
		}
		specFrom, specTo := fset.Position(sym.Spec().Pos()).Line, fset.Position(sym.Spec().End()).Line
		if line >= specFrom && line <= specTo {
			return sym, owner, true
		}
		if groupHit == nil {
			groupHit = sym
		}
	}
	if groupHit != nil {
		return groupHit, owner, true
	}
	return nil, nil, false
}

// symbolsReferencing is SymbolsReferencing's internal shape, in the
// workspace's own model types — composed here, translated to the public
// Match only at SymbolsReferencing's boundary.
func (v *View) symbolsReferencing(pkg address.PkgPath, key string) ([]match, error) {
	sym, _, ok := v.resolveSymbol(pkg, key)
	if !ok {
		return nil, fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	target := objKey(v.objectOf(sym))
	if target == "" {
		return nil, fmt.Errorf("type information unavailable for %q", sym.Key())
	}
	seen := make(map[*workspace.Symbol]bool)
	var out []match
	for _, p := range v.allPackages() {
		if err := v.ctx.Err(); err != nil {
			return nil, err
		}
		if p.TypesInfo() == nil {
			continue
		}
		for ident, obj := range p.TypesInfo().Uses {
			if objKey(obj) != target {
				continue
			}
			relFile, err := v.eng.relativePath(v.ws.FileSet().Position(ident.Pos()).Filename)
			if err != nil || relFile.EscapesRoot() {
				continue
			}
			encl, owner, ok := v.symbolFromPos(relFile, ident.Pos())
			if !ok || encl == sym || seen[encl] {
				continue
			}
			seen[encl] = true
			out = append(out, match{Pkg: owner, Sym: encl})
		}
	}
	return out, nil
}

// sortMatches orders scan hits by package, then key — determinism
// (invariant 6): Uses and Symbols are maps, and map order must never
// reach an output.
func sortMatches(matches []match) {
	slices.SortFunc(matches, func(a, b match) int {
		if c := cmp.Compare(a.Pkg.Path, b.Pkg.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Pkg.Name, b.Pkg.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.Sym.Key(), b.Sym.Key())
	})
}

// newMatches translates a slice of internal, workspace-typed scan hits
// into engine's own public shape.
func newMatches(matches []match) []Match {
	out := make([]Match, len(matches))
	for i, m := range matches {
		out[i] = Match{Pkg: newPackage(m.Pkg), Sym: newSymbol(m.Sym)}
	}
	return out
}
