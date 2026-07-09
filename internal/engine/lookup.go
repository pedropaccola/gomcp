package engine

import (
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Lookup layer: pure reads over a consistent snapshot of the workspace,
// organized in semantic sections:
//
//   - Resolvers    X(addr) (..., bool): one resource by address; comma-ok,
//                  never error. Addresses derive from resources (pkg.Path,
//                  file.Path, sym.Key()).
//   - Enumerators  Xs(scope): a scope's resources; always sorted.
//   - Scanners     workspace-wide matching, returning []Match. Scans cross
//                  packages, so hits carry their owner; enumerators don't,
//                  because the caller already holds the scope. Semantic
//                  scanners (SymbolsImplementing, SymbolsReferencing) need
//                  type information and return an error when they cannot
//                  answer exactly — approximation is never the fallback.
//   - Source       exact byte slices of Src, never re-printed.
//   - Diagnostics  problem reports, aggregated per scope.
//
// Scanners compose on enumerators, and both compose on resolvers — new
// lookups should keep to that layering. All lookups live on View and are
// only valid inside Engine.Read; pointers obtained there must not escape
// the closure.

// View is a consistent read-only snapshot of the engine state.
type View struct {
	eng *Engine
}

// Read runs fn against a consistent snapshot of the workspace. Locking lives
// here and nowhere else in the lookup layer.
func (e *Engine) Read(fn func(*View) error) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return fn(&View{eng: e})
}

// ----- Resolvers -----

// Module is the workspace's module path: the prefix of every workspace
// package address. Valid inside Read, where the snapshot is held.
func (v *View) Module() PkgPath {
	return v.eng.Module
}

// Package resolves a canonical package address to its production package.
func (v *View) Package(pkg PkgPath) (*Package, bool) {
	unit, ok := v.eng.Packages[pkg]
	if !ok || unit.Prod == nil {
		return nil, false
	}
	return unit.Prod, true
}

// XTest resolves a canonical package address to its external test package.
func (v *View) XTest(pkg PkgPath) (*Package, bool) {
	unit, ok := v.eng.Packages[pkg]
	if !ok || unit.XTest == nil {
		return nil, false
	}
	return unit.XTest, true
}

// File resolves a workspace-relative file path to the file and its owning
// package, checking the production package before the external test package.
func (v *View) File(path RelativePath) (*File, *Package, bool) {
	path = path.Clean()
	unit, ok := v.eng.Packages[v.eng.pkgAt(path.Dir())]
	if !ok {
		return nil, nil, false
	}
	for _, pkg := range []*Package{unit.Prod, unit.XTest} {
		if pkg == nil {
			continue
		}
		if file, ok := pkg.Files[path]; ok {
			return file, pkg, true
		}
	}
	return nil, nil, false
}

// Symbol resolves a package address and symbol key ("Name" or "Recv.Name")
// to the symbol and its owning package, checking Prod before XTest.
func (v *View) Symbol(pkg PkgPath, key string) (*Symbol, *Package, bool) {
	unit, ok := v.eng.Packages[pkg]
	if !ok {
		return nil, nil, false
	}
	for _, p := range []*Package{unit.Prod, unit.XTest} {
		if p == nil {
			continue
		}
		if sym, ok := p.Symbols[key]; ok {
			return sym, p, true
		}
	}
	return nil, nil, false
}

// ----- Enumerators -----

// Packages enumerates every package in the workspace: directories in path
// order, Prod before XTest.
func (v *View) Packages() []*Package {
	var out []*Package
	for _, dir := range sortedKeys(v.eng.Packages) {
		unit := v.eng.Packages[dir]
		if unit.Prod != nil {
			out = append(out, unit.Prod)
		}
		if unit.XTest != nil {
			out = append(out, unit.XTest)
		}
	}
	return out
}

// Files enumerates a package's files in path order.
func (v *View) Files(pkg *Package) []*File {
	out := make([]*File, 0, len(pkg.Files))
	for _, path := range sortedKeys(pkg.Files) {
		out = append(out, pkg.Files[path])
	}
	return out
}

// Symbols enumerates a package's symbols in key order.
func (v *View) Symbols(pkg *Package) []*Symbol {
	out := make([]*Symbol, 0, len(pkg.Symbols))
	for _, key := range sortedKeys(pkg.Symbols) {
		out = append(out, pkg.Symbols[key])
	}
	return out
}

// Methods enumerates the methods declared on typeName in one package.
func (v *View) Methods(pkg *Package, typeName string) []*Symbol {
	var out []*Symbol
	for _, sym := range v.Symbols(pkg) {
		if sym.Kind == KindMethod && sym.Recv == typeName {
			out = append(out, sym)
		}
	}
	return out
}

// ----- Scanners -----

// Match is one hit of a workspace-wide scan.
type Match struct {
	Pkg *Package
	Sym *Symbol
}

// SymbolsWhere scans every symbol in the workspace (Prod and XTest packages)
// and collects those for which pred holds. It is the primitive under every
// other scanner; new filters should compose on it as predicates.
func (v *View) SymbolsWhere(pred func(*Package, *Symbol) bool) []Match {
	var out []Match
	for _, pkg := range v.Packages() {
		for _, sym := range v.Symbols(pkg) {
			if pred(pkg, sym) {
				out = append(out, Match{Pkg: pkg, Sym: sym})
			}
		}
	}
	return out
}

// SymbolsLike scans for symbols whose key contains substr, case-insensitively.
func (v *View) SymbolsLike(substr string) []Match {
	needle := strings.ToLower(substr)
	return v.SymbolsWhere(func(_ *Package, sym *Symbol) bool {
		return strings.Contains(strings.ToLower(sym.Key()), needle)
	})
}

// SymbolsRegexp scans each symbol's own source and collects the symbols
// whose text matches re — the general-purpose matcher for when neither key
// nor name can identify the target. It searches the in-memory truth, so
// unsaved mutations are visible to it. Text outside keyed declarations
// (package clauses, imports, init bodies, floating comments) is not
// addressable by symbol and therefore not searched.
func (v *View) SymbolsRegexp(re *regexp.Regexp) []Match {
	return v.SymbolsWhere(func(_ *Package, sym *Symbol) bool {
		src, ok := v.scanSource(sym)
		return ok && re.Match(src)
	})
}

// scanSource picks the slice a text scan should see: the narrow spec for a
// symbol inside a grouped decl (so a hit attributes to one symbol, not the
// whole group), and the full declaration — keyword and doc included —
// everywhere else.
func (v *View) scanSource(sym *Symbol) ([]byte, bool) {
	if _, grouped := groupOf(sym); grouped {
		return v.SpecSource(sym)
	}
	return v.DeclSource(sym)
}

// groupOf reports whether the symbol lives inside a grouped declaration
// (const/var/type block with parentheses) and returns that declaration.
func groupOf(sym *Symbol) (*ast.GenDecl, bool) {
	gen, ok := sym.Decl.(*ast.GenDecl)
	return gen, ok && gen.Lparen.IsValid()
}

// SymbolsImplementing scans for named types whose value or pointer method
// set satisfies the given interface symbol, checked with full type
// information — embedding and promoted methods included.
func (v *View) SymbolsImplementing(sym *Symbol) ([]Match, error) {
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
	return v.SymbolsWhere(func(_ *Package, cand *Symbol) bool {
		if cand.Kind != KindType || cand == sym {
			return false
		}
		candObj := v.objectOf(cand)
		if candObj == nil {
			return false
		}
		t := candObj.Type()
		return types.Implements(t, iface) || types.Implements(types.NewPointer(t), iface)
	}), nil
}

// SymbolsReferencing scans every package's resolved identifier uses for
// references to the given symbol and reports the enclosing declarations, in
// the same address space as every other scanner. The definition itself and
// self-references (recursion) are excluded. Matching is by qualified name —
// (import path, receiver, name) — which is exact for Go and immune to the
// duplicate type-checked instances that test variants create.
func (v *View) SymbolsReferencing(sym *Symbol) ([]Match, error) {
	target := objKey(v.objectOf(sym))
	if target == "" {
		return nil, fmt.Errorf("type information unavailable for %q", sym.Key())
	}
	seen := make(map[*Symbol]bool)
	var out []Match
	for _, pkg := range v.Packages() {
		if pkg.TypesInfo == nil {
			continue
		}
		for ident, obj := range pkg.TypesInfo.Uses {
			if objKey(obj) != target {
				continue
			}
			relFile, err := v.eng.relativePath(v.eng.FileSet.Position(ident.Pos()).Filename)
			if err != nil || relFile.escapesRoot() {
				continue
			}
			encl, owner, ok := v.SymbolAt(relFile, ident.Pos())
			if !ok || encl == sym || seen[encl] {
				continue
			}
			seen[encl] = true
			out = append(out, Match{Pkg: owner, Sym: encl})
		}
	}
	sortMatches(out) // Uses is a map; iteration order must not leak out
	return out, nil
}

// SymbolAt resolves a file position to the symbol of the enclosing top-level
// declaration — the bridge from positional facts (type uses, diagnostics) to
// the declaration address space. In grouped decls it prefers the symbol
// whose own spec contains the position.
func (v *View) SymbolAt(path RelativePath, pos token.Pos) (*Symbol, *Package, bool) {
	_, owner, ok := v.File(path)
	if !ok {
		return nil, nil, false
	}
	var groupHit *Symbol
	for _, key := range sortedKeys(owner.Symbols) {
		sym := owner.Symbols[key]
		if sym.File != path {
			continue
		}
		start := sym.Decl.Pos()
		if doc := docOf(sym.Decl); doc != nil {
			start = doc.Pos()
		}
		if pos < start || pos >= sym.Decl.End() {
			continue
		}
		if sym.Spec == nil {
			return sym, owner, true
		}
		if pos >= sym.Spec.Pos() && pos < sym.Spec.End() {
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

// ----- Source -----

// DeclSource extracts the exact bytes of the symbol's whole top-level
// declaration, doc comment included. For a symbol inside a grouped decl this
// is the entire group; see SpecSource for the narrow slice.
func (v *View) DeclSource(sym *Symbol) ([]byte, bool) {
	start := sym.Decl.Pos()
	if doc := docOf(sym.Decl); doc != nil {
		start = doc.Pos()
	}
	return v.sliceSrc(sym.File, start, sym.Decl.End())
}

// SpecSource extracts the exact bytes of the symbol's own spec, doc comment
// included — the narrowest source for a symbol in a grouped decl, rendered as
// written inside the group (without the group's keyword). Falls back to
// DeclSource when the symbol has no spec.
func (v *View) SpecSource(sym *Symbol) ([]byte, bool) {
	if sym.Spec == nil {
		return v.DeclSource(sym)
	}
	start := sym.Spec.Pos()
	if doc := docOf(sym.Spec); doc != nil {
		start = doc.Pos()
	}
	return v.sliceSrc(sym.File, start, sym.Spec.End())
}

// Signature extracts a func or method header without doc comment or body.
// Comma-ok is false for every other symbol kind; compose SpecSource there.
func (v *View) Signature(sym *Symbol) ([]byte, bool) {
	fn, ok := sym.Decl.(*ast.FuncDecl)
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

// Position resolves a node to its file/line/column in the current snapshot.
func (v *View) Position(node ast.Node) token.Position {
	return v.eng.FileSet.Position(node.Pos())
}

// span is a byte-offset range [start, end) into a file's canonical Src.
type span struct{ start, end int }

// offsetSpan converts a position range into byte offsets in the file's Src —
// the primitive under both source extraction and mutation splicing. Valid
// because Ast is by invariant a parse of exactly Src.
func (v *View) offsetSpan(path RelativePath, from, to token.Pos) (span, bool) {
	file, _, ok := v.File(path)
	if !ok || !from.IsValid() || !to.IsValid() {
		return span{}, false
	}
	start := v.eng.FileSet.Position(from).Offset
	end := v.eng.FileSet.Position(to).Offset
	if start < 0 || end > len(file.Src) || start > end {
		return span{}, false
	}
	return span{start: start, end: end}, true
}

// sliceSrc cuts the exact original bytes [from, to) out of a tracked file's
// canonical source.
func (v *View) sliceSrc(path RelativePath, from, to token.Pos) ([]byte, bool) {
	file, _, ok := v.File(path)
	if !ok {
		return nil, false
	}
	sp, ok := v.offsetSpan(path, from, to)
	if !ok {
		return nil, false
	}
	return file.Src[sp.start:sp.end], true
}

// ----- Diagnostics -----

// Diagnostics aggregates one package address's package- and file-scoped
// diagnostics across its Prod and XTest packages.
func (v *View) Diagnostics(pkg PkgPath) []Diagnostic {
	unit, ok := v.eng.Packages[pkg]
	if !ok {
		return nil
	}
	var out []Diagnostic
	for _, p := range []*Package{unit.Prod, unit.XTest} {
		if p == nil {
			continue
		}
		out = append(out, p.Diags...)
		for _, file := range v.Files(p) {
			out = append(out, file.Diags...)
		}
	}
	sortDiagnostics(out)
	return out
}

// SymbolDiagnostics filters the owning file's diagnostics to those whose
// position falls inside the symbol's declaration span, doc comment included.
// It is a positional view, never the inventory: diagnostics that fall outside
// every declaration remain visible only at file scope and coarser.
func (v *View) SymbolDiagnostics(sym *Symbol) []Diagnostic {
	file, _, ok := v.File(sym.File)
	if !ok {
		return nil
	}
	start := sym.Decl.Pos()
	if doc := docOf(sym.Decl); doc != nil {
		start = doc.Pos()
	}
	from := v.eng.FileSet.Position(start).Line
	to := v.eng.FileSet.Position(sym.Decl.End()).Line
	var out []Diagnostic
	for _, diag := range file.Diags {
		if diag.Line >= from && diag.Line <= to {
			out = append(out, diag)
		}
	}
	return out
}

// WorkspaceDiagnostics enumerates only the workspace-scoped diagnostics:
// module/driver-level problems not attributable to any package.
func (v *View) WorkspaceDiagnostics() []Diagnostic {
	return slices.Clone(v.eng.Diags)
}

// AllDiagnostics aggregates workspace-scoped diagnostics followed by every
// directory's, in path order.
func (v *View) AllDiagnostics() []Diagnostic {
	out := v.WorkspaceDiagnostics()
	for _, dir := range sortedKeys(v.eng.Packages) {
		out = append(out, v.Diagnostics(dir)...)
	}
	return out
}

// ----- Internal helpers -----

// objKey identifies a types.Object semantically as "importpath:Recv.Name".
// Pointer identity is deliberately avoided: the same declaration yields
// distinct object instances in a package's plain and test-expanded variants.
func objKey(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return "" // universe scope or builtin: never a workspace symbol
	}
	name := obj.Name()
	if fn, ok := obj.(*types.Func); ok {
		if recv := fn.Signature().Recv(); recv != nil {
			if recvName := typeRecvName(recv.Type()); recvName != "" {
				name = recvName + "." + name
			}
		}
	}
	return obj.Pkg().Path() + ":" + name
}

// objectOf resolves a symbol to its types.Object via the owning package's
// Defs map; nil when type information is unavailable.
func (v *View) objectOf(sym *Symbol) types.Object {
	_, owner, ok := v.File(sym.File)
	if !ok || owner.TypesInfo == nil {
		return nil
	}
	ident := definingIdent(sym)
	if ident == nil {
		return nil
	}
	return owner.TypesInfo.Defs[ident]
}

// definingIdent returns the identifier that declares the symbol.
func definingIdent(sym *Symbol) *ast.Ident {
	if fn, ok := sym.Decl.(*ast.FuncDecl); ok {
		return fn.Name
	}
	switch spec := sym.Spec.(type) {
	case *ast.TypeSpec:
		return spec.Name
	case *ast.ValueSpec:
		for _, id := range spec.Names {
			if id.Name == sym.Name {
				return id
			}
		}
	}
	return nil
}

// typeRecvName unwraps a receiver type down to its base type name.
func typeRecvName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

func sortMatches(matches []Match) {
	slices.SortFunc(matches, func(a, b Match) int {
		if c := cmp.Compare(a.Pkg.Path, b.Pkg.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Pkg.Name, b.Pkg.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.Sym.Key(), b.Sym.Key())
	})
}

func sortDiagnostics(diags []Diagnostic) {
	slices.SortFunc(diags, func(a, b Diagnostic) int {
		if c := cmp.Compare(a.File, b.File); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Line, b.Line); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Col, b.Col); c != 0 {
			return c
		}
		return cmp.Compare(a.Msg, b.Msg)
	})
}

func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	return slices.Sorted(maps.Keys(m))
}
