package workspace

import (
	"go/ast"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
)

// File invariant: src is the canonical bytes and ast is always a parse of
// exactly src. Both are unexported so the compiler enforces it: content
// enters through Workspace.SwapFile (mutation path, parse-enforcing) or
// Package.LoadFile (load path, the type checker's own AST) — never by
// assignment.
type File struct {
	Path  address.FilePath
	src   []byte
	ast   *ast.File
	Inits []*ast.FuncDecl
	Diags []Diagnostic
	dirty bool
}

// Ast returns the parse of exactly Src.
func (f *File) Ast() *ast.File { return f.ast }

// IsDirty reports whether the file's bytes await a flush to disk.
func (f *File) IsDirty() bool { return f.dirty }

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

// newFile builds a File from already-parsed content — the one construction
// point behind File's two legitimate doors, Workspace.SwapFile and
// Package.LoadFile, so a future field never has to be kept in sync by
// hand between them.
func newFile(path address.FilePath, src []byte, astFile *ast.File, dirty bool) *File {
	return &File{Path: path, src: src, ast: astFile, dirty: dirty}
}
