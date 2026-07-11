package engine

import (
	"fmt"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine/workspace"
)

// SymbolKind classifies a top-level declaration.
type SymbolKind int

const (
	KindFunc SymbolKind = iota
	KindMethod
	KindType
	KindVar
	KindConst
)

func (k SymbolKind) String() string { return workspace.SymbolKind(k).String() }

// Symbol is engine's read-only view of one addressable top-level
// declaration: the key, kind, receiver, and owning file — copied out of
// the workspace's live model, so it carries no AST pointer and is safe to
// hold past the Read/Edit closure that produced it. Content (source text,
// diagnostics) is fetched separately, by address, through View's Source
// and Diagnostics methods.
type Symbol struct {
	key  string
	kind SymbolKind
	recv string
	file address.RelativePath
}

// newSymbol copies a workspace symbol into engine's own read-only shape.
func newSymbol(s *workspace.Symbol) Symbol {
	return Symbol{key: s.Key(), kind: SymbolKind(s.Kind), recv: s.Recv, file: s.File}
}

// Key is the symbol's address within its package: "Recv.Name" for methods,
// "Name" otherwise.
func (s Symbol) Key() string { return s.key }

// Kind classifies the declaration.
func (s Symbol) Kind() SymbolKind { return s.kind }

// Recv is the receiver type name; empty except for methods.
func (s Symbol) Recv() string { return s.recv }

// File is the workspace-relative path of the file that declares the symbol.
func (s Symbol) File() address.RelativePath { return s.file }

// File is engine's read-only view of one file's presentation-relevant
// facts: its path and its own package-doc comment, copied out of the
// workspace's live model so it carries no pointer into the live model.
type File struct {
	path address.RelativePath
	doc  string
}

// newFile copies a workspace file's read-only facts into engine's own shape.
func newFile(f *workspace.File) File {
	return File{path: f.Path, doc: f.Doc()}
}

// Path is the file's workspace-relative address.
func (f File) Path() address.RelativePath { return f.path }

// Package is engine's read-only view of one compiled package: its address,
// files, symbols, and godoc, copied out of the workspace's live model so it
// carries no pointer into the live model (no AST, no type-checker output).
type Package struct {
	path    address.RelativePath
	pkgPath address.PkgPath
	files   []File
	symbols []Symbol
	doc     string
}

// newPackage copies a workspace package's read-only facts into engine's
// own shape: its files and symbols, translated recursively.
func newPackage(p *workspace.Package) Package {
	wf := p.Files()
	files := make([]File, len(wf))
	for i, f := range wf {
		files[i] = newFile(f)
	}
	ws := p.Symbols()
	symbols := make([]Symbol, len(ws))
	for i, s := range ws {
		symbols[i] = newSymbol(s)
	}
	return Package{path: p.Path, pkgPath: p.PkgPath, files: files, symbols: symbols, doc: p.Doc()}
}

// Path is the package's workspace directory (empty for a dependency).
func (p Package) Path() address.RelativePath { return p.path }

// PkgPath is the package's import path: its canonical address.
func (p Package) PkgPath() address.PkgPath { return p.pkgPath }

// Files enumerates the package's files, sorted by path.
func (p Package) Files() []File { return p.files }

// Symbols enumerates the package's symbols, sorted by key.
func (p Package) Symbols() []Symbol { return p.symbols }

// Symbol resolves one symbol by key ("Name" or "Recv.Name") within the
// package.
func (p Package) Symbol(key string) (Symbol, bool) {
	for _, s := range p.symbols {
		if s.key == key {
			return s, true
		}
	}
	return Symbol{}, false
}

// DiagKind classifies a problem report by its source in the load pipeline.
type DiagKind int

const (
	DiagUnknown DiagKind = iota
	DiagList
	DiagParse
	DiagType
)

// Doc is the package's godoc — every file's own doc comment, concatenated
// in file order.
func (p Package) Doc() string { return p.doc }

// Doc is the file's own package-doc comment text, or "" when it has none.
func (f File) Doc() string { return f.doc }

func (k DiagKind) String() string { return workspace.DiagKind(k).String() }

// Diagnostic is a source-agnostic problem report: engine's own copy of the
// workspace's finding, safe to hold past the Read/Edit closure that
// produced it since it carries no pointer into the live model. Attribution
// is by declaration, not position: Package/Key name the enclosing
// declaration when one resolves, File is the coarser fallback for a
// diagnostic attributable to a file but no single declaration (import
// blocks, unparsed syntax), and both are empty for module/driver-level
// problems.
type Diagnostic struct {
	File    address.RelativePath // "" when not attributable to a workspace file
	Package address.PkgPath      // "" when not attributable to a package
	Key     string               // enclosing declaration's key; "" when not attributable to one
	Kind    DiagKind
	Msg     string
}

func (d Diagnostic) String() string {
	switch {
	case d.Key != "":
		return fmt.Sprintf("[%s] %s.%s: %s", d.Kind, d.Package, d.Key, d.Msg)
	case d.File != "":
		return fmt.Sprintf("[%s] %s: %s", d.Kind, d.File, d.Msg)
	default:
		return fmt.Sprintf("[%s] %s", d.Kind, d.Msg)
	}
}

// newDiagnostic copies one workspace diagnostic into engine's own shape,
// attributing it to pkg/key when the caller has already resolved them.
func newDiagnostic(d workspace.Diagnostic, pkg address.PkgPath, key string) Diagnostic {
	return Diagnostic{File: d.File, Package: pkg, Key: key, Kind: DiagKind(d.Kind), Msg: d.Msg}
}

// newDiagnostics copies a slice of workspace diagnostics into engine's own
// shape, all sharing the same pkg/key attribution (e.g. every diagnostic
// inside one symbol's span), preserving nil for an empty slice.
func newDiagnostics(ds []workspace.Diagnostic, pkg address.PkgPath, key string) []Diagnostic {
	if ds == nil {
		return nil
	}
	out := make([]Diagnostic, len(ds))
	for i, d := range ds {
		out[i] = newDiagnostic(d, pkg, key)
	}
	return out
}
