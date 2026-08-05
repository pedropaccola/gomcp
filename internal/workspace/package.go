package workspace

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"path"
	"slices"
	"strings"
)

const (
	KindProd PackageKind = iota
	KindXTest
	KindExternal
)

var packageKindNames = [...]string{"prod", "xtest", "external"}

// Package is one compiled package of the model: files, the derived symbol
// index, diagnostics, and the type checker's output. Files, the index, and
// the type checker's output are unexported with sorted-only or gated
// accessors — determinism and containment by construction; they change
// only through the Workspace primitives, RebuildIndex, and NewPackage.
// symbols is keyed by name (or Recv.Name for methods) but multi-valued:
// two files can legitimately declare the same top-level name when at
// least one is Ignored — a real build would never let both survive, but
// this model has to represent both to avoid silently losing one. See
// RebuildIndex for the resolution order within each key's slice.
type Package struct {
	Name string
	ID   PackageID // identity: canonical path plus Prod/XTest/External

	files   map[FilePath]*File
	symbols map[string][]*Symbol // derived index; see RebuildIndex
	Diags   []Diagnostic         // package-scoped: no usable file position

	// Type information for the whole package, nil when type-checking could
	// not run at all. Populated per load; a broken package still gets
	// partial info alongside its DiagType diagnostics. Reach outside this
	// package only through Types()/TypesInfo().
	typesPkg  *types.Package
	typesInfo *types.Info
}

// NewPackage constructs a package from raw, already-trusted identity
// pieces (path, kind) plus its type-checker output — the load path's
// door for a fresh Package, building the sealed ID internally so callers
// outside this package (disk's buildPackage/buildExternal) never need
// their own way to mint one. Also the door for typesPkg/typesInfo: direct
// struct literals from outside this package can no longer set them now
// that they're sealed.
func NewPackage(name string, path PackagePath, kind PackageKind, typesPkg *types.Package, typesInfo *types.Info) *Package {
	return &Package{
		Name:      name,
		ID:        newPackageID(path, kind),
		typesPkg:  typesPkg,
		typesInfo: typesInfo,
	}
}

// LoadFile installs bytes with the loader's AST as a clean file — the
// load path's door for content, where the AST is the one the type checker
// saw and is stored as-is; SwapFile is the mutation path's door.
func (p *Package) LoadFile(path FilePath, src []byte, astFile *ast.File, ignored bool) {
	if p.files == nil {
		p.files = make(map[FilePath]*File)
	}
	p.files[path] = newFile(path, p.ID, src, astFile, false, ignored)
}

// Clone copies the package shallowly with fresh maps; File values are
// shared and treated as immutable — mutations install fresh *File instances.
func (p *Package) Clone() *Package {
	cloned := *p
	cloned.files = maps.Clone(p.files)
	cloned.symbols = maps.Clone(p.symbols)
	return &cloned
}

// cloneShell copies the package's metadata with no files and an empty
// index — the starting point for relocations that re-admit every file
// through the content pipeline.
func (p *Package) cloneShell() *Package {
	shell := *p
	shell.files = make(map[FilePath]*File, len(p.files))
	shell.symbols = make(map[string][]*Symbol)
	return &shell
}

// File resolves one file by path.
func (p *Package) File(path FilePath) (*File, bool) {
	file, ok := p.files[path]
	return file, ok
}

// Files enumerates the package's files in path order.
func (p *Package) Files() []*File {
	out := make([]*File, 0, len(p.files))
	for _, path := range slices.Sorted(maps.Keys(p.files)) {
		out = append(out, p.files[path])
	}
	return out
}

// RebuildIndex re-derives symbols and every file's Inits from the current
// ASTs, and stamps each symbol's Owner and Ignored — the constructing
// Package's own ID and the symbol's own owning File's Ignored bit —
// since IndexAST itself stays reusable on bare, unowned ASTs
// (classifyFragment's scratch parsing has no real owner/file to give
// it). Then, within each name's slice, sorts active (non-Ignored)
// entries before Ignored ones, and by file path as a deterministic
// tiebreak within either group — the resolution order Package.Symbol's
// single-result lookup relies on: slice[0] is always the entry an active
// build would actually keep, when one exists. Call after any file's ast
// is replaced; nothing is patched in place. For an external (dependency)
// package, this also strips every symbol from the result that isn't
// reachable from outside the package: an unexported symbol by its own
// name, or a method whose name is exported but whose receiver type
// isn't — the receiver type can't be named from outside the package, so
// no external caller could ever hold a value to call the method on. A
// dependency is API surface only, never editable code, so nothing
// outside that reachable surface is indexed at all.
func (p *Package) RebuildIndex() {
	p.symbols = make(map[string][]*Symbol)
	for _, file := range p.files {
		syms, inits := file.Symbols()
		file.Inits = inits
		for _, sym := range syms {
			p.symbols[sym.Key()] = append(p.symbols[sym.Key()], sym)
		}
	}
	for _, syms := range p.symbols {
		for _, sym := range syms {
			sym.Owner = p.ID
			if file, ok := p.files[sym.File]; ok {
				sym.Ignored = file.Ignored
			}
		}
		slices.SortFunc(syms, func(a, b *Symbol) int {
			if a.Ignored != b.Ignored {
				if a.Ignored {
					return 1
				}
				return -1
			}
			return cmp.Compare(a.File, b.File)
		})
	}
	if p.ID.Kind() != KindExternal {
		return
	}
	for key, syms := range p.symbols {
		var kept []*Symbol
		for _, sym := range syms {
			if token.IsExported(sym.Name) && (sym.Recv == "" || token.IsExported(sym.Recv)) {
				kept = append(kept, sym)
			}
		}
		if len(kept) == 0 {
			delete(p.symbols, key)
		} else {
			p.symbols[key] = kept
		}
	}
}

// Symbol resolves one symbol by key ("Name" or "Recv.Name") to its
// primary declaration — the one an active build would actually keep, per
// RebuildIndex's own sort. For internal, non-file-aware callers (finders,
// move/rename logic) that have no way to disambiguate a same-keyed
// collision themselves; a caller with a specific file in hand should
// resolve through Workspace.ResolveSymbolIn instead.
func (p *Package) Symbol(key string) (*Symbol, bool) {
	syms, ok := p.symbols[key]
	if !ok || len(syms) == 0 {
		return nil, false
	}
	return syms[0], true
}

// Symbols enumerates every declaration in the package, in key order —
// every entry under a colliding key included, not just the primary, so a
// full scan never silently under-reports a real declaration.
func (p *Package) Symbols() []*Symbol {
	var out []*Symbol
	for _, key := range slices.Sorted(maps.Keys(p.symbols)) {
		out = append(out, p.symbols[key]...)
	}
	return out
}

// Types returns the package's whole-package type-checker output, nil when
// type-checking could not run at all.
func (p *Package) Types() *types.Package { return p.typesPkg }

// TypesInfo returns the package's resolved identifier/type facts, nil when
// type-checking could not run at all.
func (p *Package) TypesInfo() *types.Info { return p.typesInfo }

// Doc returns the package's godoc: every active file's own doc comment,
// concatenated in file order — documentation lives distributed across a
// package's files, not centralized in one. An Ignored file's own doc
// comment is excluded: it's not part of the package's real, buildable
// documented surface.
func (p *Package) Doc() string {
	files := p.Files()
	parts := make([]string, 0, len(files))
	for _, f := range files {
		if f.Ignored {
			continue
		}
		if doc := f.Doc(); doc != "" {
			parts = append(parts, doc)
		}
	}
	return strings.Join(parts, "\n\n")
}

// MarkFlushed clears path's dirty mark by installing a fresh copy of its
// File — Flush's half of the dirty lifecycle; SwapFile and MoveFile set
// the mark. Replaces rather than mutates in place, since a File may still
// be shared with another Workspace generation via Clone.
func (p *Package) MarkFlushed(path FilePath) {
	if file, ok := p.files[path]; ok {
		cp := *file
		cp.dirty = false
		p.files[path] = &cp
	}
}

// Relocated returns a detached shell of p as it becomes after a package
// move to newPkg: same shape as cloneShell, but with ID rewritten to the
// destination address (kind preserved), and Name rewritten too when
// renameName — the one place a package's own identity-under-move is
// derived.
func (p *Package) Relocated(newPkg PackagePath, renameName bool) *Package {
	moved := p.cloneShell()
	moved.ID = newPackageID(newPkg, p.ID.Kind())
	if renameName {
		moved.Name = newPkg.Base() + strings.TrimPrefix(p.Name, p.ID.Base().Base())
	}
	return moved
}

// fileContaining finds which of p's files owns pos — positions never
// overlap across files in a shared FileSet, so a range check against
// each file's own AST span is sufficient, no path translation needed.
func (p *Package) fileContaining(pos token.Pos) (*File, bool) {
	for _, f := range p.Files() {
		if f.Ast().Pos() <= pos && pos < f.Ast().End() {
			return f, true
		}
	}
	return nil, false
}

// objectOf resolves sym to its types.Object via p's Defs map; nil when
// type information is unavailable.
func (p *Package) objectOf(sym *Symbol) types.Object {
	if p.TypesInfo() == nil {
		return nil
	}
	ident := sym.DefiningIdent()
	if ident == nil {
		return nil
	}
	return p.TypesInfo().Defs[ident]
}

// symbolAt finds p's top-level declaration enclosing pos. No
// filesystem-path round trip needed, since every caller here already has
// p in hand from the loop that found pos in the first place —
// go/token positions never overlap across files in a shared FileSet, so
// no per-file filter is needed for correctness. In grouped decls it
// prefers the symbol whose own spec contains the position.
func (p *Package) symbolAt(pos token.Pos) (*Symbol, bool) {
	var groupHit *Symbol
	for _, sym := range p.Symbols() {
		start := sym.Decl().Pos()
		if doc := DocOf(sym.Decl()); doc != nil {
			start = doc.Pos()
		}
		if pos < start || pos >= sym.Decl().End() {
			continue
		}
		if sym.Spec() == nil {
			return sym, true
		}
		if pos >= sym.Spec().Pos() && pos < sym.Spec().End() {
			return sym, true
		}
		if groupHit == nil {
			groupHit = sym
		}
	}
	if groupHit != nil {
		return groupHit, true
	}
	return nil, false
}

// Diagnostics aggregates p's own package-scoped diagnostics with every
// one of its files' own file-scoped diagnostics — the whole-package view
// a caller merging several members (Prod and XTest) needs per member,
// before combining and sorting the result for presentation.
func (p *Package) Diagnostics() []Diagnostic {
	var out []Diagnostic
	out = append(out, p.Diags...)
	for _, file := range p.Files() {
		out = append(out, file.Diags...)
	}
	return out
}

// DetectEditCollisions reports which of newKeys already exist elsewhere in
// p, blocking a replacement of key — a same-group sibling doesn't count
// when key is itself position-dependent, since resubmitting the whole
// group necessarily re-mentions every member.
func (p *Package) DetectEditCollisions(key string, newKeys []string) []string {
	sym, ok := p.Symbol(key)
	if !ok {
		return nil
	}
	gen, grouped := sym.GroupOf()
	wasPositionDependent := constPositionDependent(gen, grouped, sym)
	var collisions []string
	for _, newKey := range newKeys {
		if newKey == key || newKey == "init" {
			continue
		}
		existing, exists := p.Symbol(newKey)
		if !exists {
			continue
		}
		if wasPositionDependent {
			if eGen, eGrouped := existing.GroupOf(); eGrouped && eGen == gen {
				continue
			}
		}
		collisions = append(collisions, newKey)
	}
	return collisions
}

// PositionDependentGroupMembers returns every key that must move or
// extract together with key: itself alone, unless key is a member of a
// grouped const declaration whose meaning is position-dependent (iota,
// or inheriting the previous spec's expression) — in which case every
// member of that group is included, the same set ExtractDeclaration
// itself acts on for such a member. Deliberately narrow: var and type
// groups, and non-position-dependent const groups, are grouped in
// source for readability only — nothing about them requires moving
// together, so they are never expanded here.
func (p *Package) PositionDependentGroupMembers(key string) ([]string, error) {
	sym, ok := p.Symbol(key)
	if !ok {
		return nil, NoSymbolError(key, p.ID.Base())
	}
	gen, grouped := sym.GroupOf()
	if !grouped || (!isSoloGroup(gen, grouped) && !constPositionDependent(gen, grouped, sym)) {
		return []string{key}, nil
	}
	var members []string
	for _, s := range p.Symbols() {
		if g, ok := s.GroupOf(); ok && g == gen {
			members = append(members, s.Key())
		}
	}
	return members, nil
}

// SwapFile installs formatted/astFile as a fresh dirty File at path and
// rebuilds p's own index — the install half of the mutation path.
// Formatting and parsing (which can fail, and must not fork a package
// needlessly on failure) happen in the caller first; by the time this is
// called, both have already succeeded.
func (p *Package) SwapFile(path FilePath, ignored bool, formatted []byte, astFile *ast.File) {
	if p.files == nil {
		p.files = make(map[FilePath]*File)
	}
	p.files[path] = newFile(path, p.ID, formatted, astFile, true, ignored)
	p.RebuildIndex()
}

// ReferencesTo finds every symbol in p whose declaration resolves a
// reference to target (an ObjectKey identity), excluding exclude and
// any symbol already present in seen — seen is the caller's, not p's
// own, since a scan spans many packages and dedup happens across all of
// them, not within one; entries this call finds are added to it. Only
// matches package-level declarations and methods — see
// isPackageLevelUse's doc comment for why that guard matters.
func (p *Package) ReferencesTo(target ObjectKey, exclude *Symbol, seen map[*Symbol]bool) []*Symbol {
	if p.TypesInfo() == nil {
		return nil
	}
	var out []*Symbol
	for ident, obj := range p.TypesInfo().Uses {
		if !isPackageLevelUse(obj) {
			continue
		}
		key, ok := keyOf(obj)
		if !ok || key != target {
			continue
		}
		encl, ok := p.symbolAt(ident.Pos())
		if !ok || encl == exclude || seen[encl] {
			continue
		}
		seen[encl] = true
		out = append(out, encl)
	}
	return out
}

// PackagePath is a package's canonical import path — always unsuffixed,
// by construction: nothing produces a PackagePath carrying go/packages'
// own "_test" convention for an external-test half. This is what every
// workspace address is keyed by, what Workspace's module root is, and
// the "path" component sealed inside PackageID — an address is
// inherently kind-agnostic (it may back both a Prod and an XTest Package
// at one directory), so its own key can never meaningfully carry a kind.
type PackagePath string

// Base is p's bare final component — the package's own directory name,
// stripped of everything before it (including the module prefix, since
// that's just another leading component). path.Base, not filepath.Base:
// an address's separator is always "/", regardless of the host OS.
func (p PackagePath) Base() string {
	return path.Base(string(p))
}

// File composes a file's FilePath from an already-known-legitimate bare
// name inside p — trusted, no validation, always succeeds. Every
// FilePath is built from a canonical PackagePath alone: a file's own
// address never carries the XTest distinction, since internal_test.go
// and external_test.go can live in the identical directory, addressed
// the same way — only the file's own package clause says which half
// owns it.
func (p PackagePath) File(name string) FilePath {
	return FilePath(p.String() + "/" + name)
}

// Join composes a subpackage's PackagePath from an already-known-legitimate
// workspace-relative directory — trusted, no validation, always
// succeeds. For untrusted agent input, use NewPackageID.
func (p PackagePath) Join(dir string) PackagePath {
	if dir == "" || dir == "." {
		return p
	}
	return PackagePath(p.String() + "/" + dir)
}

func (p PackagePath) String() string { return string(p) }

// PackageID names one specific package variant — Prod, XTest, or
// External — never constructible in a state where its own path and kind
// could disagree, since NewPackageID (untrusted, agent-supplied text)
// and newPackageID (trusted, already-known-good pieces) are the only
// doors. This is the type that replaces a bare address everywhere a
// mismatched-suffix bug was possible: Package.ID, and every store/tools
// signature that names one resolved-or-being-resolved package.
type PackageID struct {
	path PackagePath
	kind PackageKind
}

// Base is id's canonical path, kind stripped — for member lookups and
// file construction, which are inherently kind-agnostic.
func (id PackageID) Base() PackagePath { return id.path }

// Kind reports whether id names a Prod, XTest, or External package.
func (id PackageID) Kind() PackageKind { return id.kind }

// String recomposes id's full spelling — go/packages' own "_test"
// suffix convention on an XTest half, the bare path otherwise. The
// external contract every tool's JSON output already uses.
func (id PackageID) String() string {
	if id.kind == KindXTest {
		return string(id.path) + "_test"
	}
	return string(id.path)
}

// PackageKind classifies what a package is relative to the workspace:
// its own production or external-test half, or a read-only dependency
// from the module cache. Mutually exclusive by construction — closes the
// illegal state IsXTest and External as two independent bools allowed
// (both true simultaneously), even though no code path ever produced it.
type PackageKind int

// String returns k's lowercase name.
func (k PackageKind) String() string {
	return packageKindNames[k]
}

// NewPackagePath narrows addr, an untrusted agent-supplied package
// address, against module: module-prefixed addresses pass through, bare
// workspace directories gain the prefix. Returns the canonical address
// alone — an agent never specifies which internal kind (Prod/XTest/
// Ignored) it means; resolution across all three happens internally
// (Workspace.MembersOf and its callers), and a file name is already
// unique per directory regardless of which kind loaded it, so no suffix
// is needed to disambiguate anywhere on this side of the boundary. File
// names are refused here — packages are directories, always spelled
// alone.
func NewPackagePath(module PackagePath, addr string) (PackagePath, error) {
	rel, ok := cleanRelative(module, addr)
	if !ok {
		return "", fmt.Errorf("invalid package path %q", addr)
	}
	if strings.HasSuffix(rel, ".go") {
		return "", fmt.Errorf("%q names a file: a package address must name a directory alone", addr)
	}
	full := string(module)
	if rel != "." {
		full = string(module) + "/" + rel
	}
	return PackagePath(full), nil
}

// newPackageID builds an already-validated identity directly from a
// canonical path and kind — the trusted door used only by workspace's
// own construction paths (NewPackage, disk's ingestion), which already
// know both pieces agree. Narrowing untrusted agent text belongs to
// NewPackageID alone.
func newPackageID(path PackagePath, kind PackageKind) PackageID {
	return PackageID{path: path, kind: kind}
}
