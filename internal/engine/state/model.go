package state

import (
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"slices"
)

// Symbol is one addressable top-level declaration: the unit of every read
// and every mutation. Decl (and Spec, for grouped members) point into the
// owning file's Ast, which by invariant parses exactly its Src — so a
// Symbol locates byte spans but never survives a rebuild (see
// RebuildIndex).
type Symbol struct {
	Name string
	File RelativePath
	Kind SymbolKind
	Recv string   // receiver type name; set only for KindMethod
	Decl ast.Decl // the top-level declaration: the splice point for mutations
	Spec ast.Spec // the symbol's own spec when Decl is a grouped GenDecl
}

// Key is the symbol-index key: "Recv.Name" for methods, Name otherwise.
func (s *Symbol) Key() string {
	if s.Kind == KindMethod && s.Recv != "" {
		return s.Recv + "." + s.Name
	}
	return s.Name
}

// Doc is derived from the AST on demand so it cannot go stale after mutations.
// The doc on the individual spec wins over the grouped declaration's doc.
func (s *Symbol) Doc() string {
	if s.Spec != nil {
		if text := DocOf(s.Spec).Text(); text != "" {
			return text
		}
	}
	return DocOf(s.Decl).Text()
}

// File invariant: src is the canonical bytes and ast is always a parse of
// exactly src. Both are unexported so the compiler enforces it: content
// enters through Workspace.SwapFile (mutation path, parse-enforcing) or
// Package.AddLoadedFile (load path, the type checker's own AST) — never by
// assignment.
type File struct {
	Path  RelativePath
	src   []byte
	ast   *ast.File
	Inits []*ast.FuncDecl
	Diags []Diagnostic
	dirty bool
}

// Src returns the file's canonical bytes.
func (f *File) Src() []byte { return f.src }

// Ast returns the parse of exactly Src.
func (f *File) Ast() *ast.File { return f.ast }

// Dirty reports whether the file's bytes await a flush to disk.
func (f *File) Dirty() bool { return f.dirty }

// MarkFlushed clears the dirty mark once the bytes reached disk — Flush's
// half of the dirty lifecycle; SwapFile and MoveFile set the mark.
func (f *File) MarkFlushed() { f.dirty = false }

// Package is one compiled package of the model: files, the derived symbol
// index, diagnostics, and the type checker's output. Files and the index
// are unexported with sorted-only accessors — determinism by construction;
// they change only through the Workspace primitives and RebuildIndex.
type Package struct {
	Name    string
	Path    RelativePath // workspace directory: the disk location
	PkgPath PkgPath      // import path: the canonical address
	files   map[RelativePath]*File
	symbols map[string]*Symbol // derived index; see RebuildIndex
	Diags   []Diagnostic       // package-scoped: no usable file position

	// Type information for the whole package, nil when type-checking could
	// not run at all. Populated per load; a broken package still gets
	// partial info alongside its DiagType diagnostics.
	Types     *types.Package
	TypesInfo *types.Info

	// External marks a read-only dependency from the module cache: its
	// positions live in a dedicated FileSet, its files are addressed by
	// import-path-qualified pseudo-paths, and it is never mutated or
	// flushed.
	External bool
}

// RebuildIndex re-derives symbols and every file's Inits from the current
// ASTs. Call after any file's ast is replaced; nothing is patched in place.
// External packages keep exported symbols only — a dependency is API
// surface, not editable code.
func (p *Package) RebuildIndex() {
	p.symbols = make(map[string]*Symbol)
	for _, file := range p.files {
		file.Inits = IndexAST(file.Path, file.ast, p.symbols)
	}
	if !p.External {
		return
	}
	for key, sym := range p.symbols {
		if !token.IsExported(sym.Name) || (sym.Recv != "" && !token.IsExported(sym.Recv)) {
			delete(p.symbols, key)
		}
	}
}

// Clone copies the package shallowly with fresh maps; File values are
// shared and treated as immutable — mutations install fresh *File instances.
func (p *Package) Clone() *Package {
	cloned := *p
	cloned.files = maps.Clone(p.files)
	cloned.symbols = maps.Clone(p.symbols)
	return &cloned
}

// File resolves one file by path.
func (p *Package) File(path RelativePath) (*File, bool) {
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

// Symbol resolves one symbol by key ("Name" or "Recv.Name").
func (p *Package) Symbol(key string) (*Symbol, bool) {
	sym, ok := p.symbols[key]
	return sym, ok
}

// Symbols enumerates the package's symbols in key order.
func (p *Package) Symbols() []*Symbol {
	out := make([]*Symbol, 0, len(p.symbols))
	for _, key := range slices.Sorted(maps.Keys(p.symbols)) {
		out = append(out, p.symbols[key])
	}
	return out
}

// AddLoadedFile installs bytes with the loader's AST as a clean file — the
// load path's door for content, where the AST is the one the type checker
// saw and is stored as-is; SwapFile is the mutation path's door.
func (p *Package) AddLoadedFile(path RelativePath, src []byte, astFile *ast.File) {
	if p.files == nil {
		p.files = make(map[RelativePath]*File)
	}
	p.files[path] = &File{Path: path, src: src, ast: astFile}
}

// CloneShell copies the package's metadata with no files and an empty
// index — the starting point for relocations that re-admit every file
// through the content pipeline.
func (p *Package) CloneShell() *Package {
	shell := *p
	shell.files = make(map[RelativePath]*File, len(p.files))
	shell.symbols = make(map[string]*Symbol)
	return &shell
}

// Unit holds the packages of one workspace address: the production package
// (with in-package test files folded in) and the external _test package.
type Unit struct {
	Prod  *Package
	XTest *Package
}

// MarkDirty re-marks path dirty in whichever of the unit's packages holds
// it — how dirty state survives a reload built from overlays.
func (u *Unit) MarkDirty(path RelativePath) {
	for _, p := range []*Package{u.Prod, u.XTest} {
		if p == nil {
			continue
		}
		if file, ok := p.files[path]; ok {
			file.dirty = true
		}
	}
}

// DocOf returns the doc comment attached to a declaration or spec, nil when
// there is none. The single authority on where a node's documentation lives —
// extraction and mutation splicing must agree on it.
func DocOf(node ast.Node) *ast.CommentGroup {
	switch n := node.(type) {
	case *ast.FuncDecl:
		return n.Doc
	case *ast.GenDecl:
		return n.Doc
	case *ast.TypeSpec:
		return n.Doc
	case *ast.ValueSpec:
		return n.Doc
	}
	return nil
}

// RecvTypeName unwraps a method receiver down to its base type name,
// handling pointer, parenthesized, and generic (T[P]) receivers.
func RecvTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	typ := recv.List[0].Type
	for {
		switch t := typ.(type) {
		case *ast.StarExpr:
			typ = t.X
		case *ast.ParenExpr:
			typ = t.X
		case *ast.IndexExpr:
			typ = t.X
		case *ast.IndexListExpr:
			typ = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// IndexAST fills symbols with astFile's top-level declarations, attributed
// to path, and returns its init functions — the one indexer, behind
// RebuildIndex and usable on bare ASTs that have no model File.
func IndexAST(path RelativePath, astFile *ast.File, symbols map[string]*Symbol) (inits []*ast.FuncDecl) {
	for _, decl := range astFile.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			if node.Recv == nil && node.Name.Name == "init" {
				inits = append(inits, node)
				continue
			}
			sym := &Symbol{
				Name: node.Name.Name,
				File: path,
				Kind: KindFunc,
				Decl: node,
			}
			if node.Recv != nil {
				sym.Kind = KindMethod
				sym.Recv = RecvTypeName(node.Recv)
			}
			symbols[sym.Key()] = sym
		case *ast.GenDecl:
			switch node.Tok {
			case token.TYPE:
				for _, spec := range node.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						symbols[typeSpec.Name.Name] = &Symbol{
							Name: typeSpec.Name.Name,
							File: path,
							Kind: KindType,
							Decl: node,
							Spec: typeSpec,
						}
					}
				}
			case token.VAR, token.CONST:
				symbolKind := KindVar
				if node.Tok == token.CONST {
					symbolKind = KindConst
				}
				for _, spec := range node.Specs {
					if valueSpec, ok := spec.(*ast.ValueSpec); ok {
						for _, id := range valueSpec.Names {
							if id.Name == "_" {
								continue
							}
							symbols[id.Name] = &Symbol{
								Name: id.Name,
								File: path,
								Kind: symbolKind,
								Decl: node,
								Spec: valueSpec,
							}
						}
					}
				}
			}
		}
	}
	return inits
}
