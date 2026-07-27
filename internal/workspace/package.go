package workspace

import (
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
)

// Package is one compiled package of the model: files, the derived symbol
// index, diagnostics, and the type checker's output. Files, the index, and
// the type checker's output are unexported with sorted-only or gated
// accessors — determinism and containment by construction; they change
// only through the Workspace primitives, RebuildIndex, and NewPackage.
type Package struct {
	Name    string
	Path    address.RelativePath // workspace directory: the disk location
	PkgPath address.PkgPath      // import path: the canonical address
	files   map[address.RelativePath]*File
	symbols map[string]*Symbol // derived index; see RebuildIndex
	Diags   []Diagnostic       // package-scoped: no usable file position

	// Type information for the whole package, nil when type-checking could
	// not run at all. Populated per load; a broken package still gets
	// partial info alongside its DiagType diagnostics. Reach outside this
	// package only through Types()/TypesInfo().
	typesPkg  *types.Package
	typesInfo *types.Info

	// External marks a read-only dependency from the module cache: its
	// positions live in a dedicated FileSet, its files are addressed by
	// import-path-qualified pseudo-paths, and it is never mutated or
	// flushed.
	External bool
}

// NewPackage constructs a package with its type-checker output, the load
// path's other door for the fields NewPackage/LoadFile own — direct
// struct literals from outside this package can no longer set typesPkg/
// typesInfo now that they're sealed.
func NewPackage(name string, path address.RelativePath, pkgPath address.PkgPath, typesPkg *types.Package, typesInfo *types.Info, external bool) *Package {
	return &Package{
		Name:      name,
		Path:      path,
		PkgPath:   pkgPath,
		typesPkg:  typesPkg,
		typesInfo: typesInfo,
		External:  external,
	}
}

// LoadFile installs bytes with the loader's AST as a clean file — the
// load path's door for content, where the AST is the one the type checker
// saw and is stored as-is; SwapFile is the mutation path's door.
func (p *Package) LoadFile(path address.RelativePath, src []byte, astFile *ast.File) {
	if p.files == nil {
		p.files = make(map[address.RelativePath]*File)
	}
	p.files[path] = newFile(path, src, astFile, false)
}

// Clone copies the package shallowly with fresh maps; File values are
// shared and treated as immutable — mutations install fresh *File instances.
func (p *Package) Clone() *Package {
	cloned := *p
	cloned.files = maps.Clone(p.files)
	cloned.symbols = maps.Clone(p.symbols)
	return &cloned
}

// CloneShell copies the package's metadata with no files and an empty
// index — the starting point for relocations that re-admit every file
// through the content pipeline.
func (p *Package) CloneShell() *Package {
	shell := *p
	shell.files = make(map[address.RelativePath]*File, len(p.files))
	shell.symbols = make(map[string]*Symbol)
	return &shell
}

// File resolves one file by path.
func (p *Package) File(path address.RelativePath) (*File, bool) {
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

// Types returns the package's whole-package type-checker output, nil when
// type-checking could not run at all.
func (p *Package) Types() *types.Package { return p.typesPkg }

// TypesInfo returns the package's resolved identifier/type facts, nil when
// type-checking could not run at all.
func (p *Package) TypesInfo() *types.Info { return p.typesInfo }

// Doc returns the package's godoc: every file's own doc comment,
// concatenated in file order — documentation lives distributed across a
// package's files, not centralized in one.
func (p *Package) Doc() string {
	files := p.Files()
	parts := make([]string, 0, len(files))
	for _, f := range files {
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
func (p *Package) MarkFlushed(path address.RelativePath) {
	if file, ok := p.files[path]; ok {
		cp := *file
		cp.dirty = false
		p.files[path] = &cp
	}
}
