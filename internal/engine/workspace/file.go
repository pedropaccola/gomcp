package workspace

import (
	"go/ast"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
)

// File invariant: src is the canonical bytes and ast is always a parse of
// exactly src. Both are unexported so the compiler enforces it: content
// enters through Workspace.SwapFile (mutation path, parse-enforcing) or
// Package.AddLoadedFile (load path, the type checker's own AST) — never by
// assignment.
type File struct {
	Path  address.RelativePath
	src   []byte
	ast   *ast.File
	Inits []*ast.FuncDecl
	Diags []Diagnostic
	dirty bool
}

// Ast returns the parse of exactly Src.
func (f *File) Ast() *ast.File { return f.ast }

// Dirty reports whether the file's bytes await a flush to disk.
func (f *File) Dirty() bool { return f.dirty }

// Src returns the file's canonical bytes.
func (f *File) Src() []byte { return f.src }

// Doc returns the file's own package-doc comment text — the comment block
// directly above the package clause — or "" when it has none.
func (f *File) Doc() string {
	if f.ast.Doc == nil {
		return ""
	}
	return strings.TrimSpace(f.ast.Doc.Text())
}
