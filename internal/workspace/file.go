package workspace

import (
	"fmt"
	"go/ast"
	"path"
	"path/filepath"
	"strings"
)

// File invariant: src is the canonical bytes and ast is always a parse of
// exactly src. Both are unexported so the compiler enforces it: content
// enters through Workspace.SwapFile (mutation path, parse-enforcing) or
// Package.LoadFile (load path, the type checker's own AST) — never by
// assignment.
type File struct {
	Path  FilePath
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

// FilePath is a workspace-relative disk path: the address form of the
// disk boundary (files, tombstones, flush, the recheck overlay), while
// PackagePath/PackageID are the address forms of everything else.
// Ownership isn't derivable from a file's own address alone —
// internal_test.go and external_test.go can live in the identical
// directory, addressed the same way; only parsing the file's own package
// clause tells you which package owns it. Untrusted strings enter
// through NewFilePath.
type FilePath string

// Base is f's bare file name — for presentation alongside a package
// address shown separately, or as the raw material for composing a new
// FilePath (PackagePath.File). Never an address on its own. path.Base,
// not filepath.Base: an address's separator is always "/", regardless
// of the host OS.
func (f FilePath) Base() string {
	return path.Base(string(f))
}

// PackagePath is the canonical package address f belongs to. Always
// already the exact address that built f (every FilePath is constructed
// as pkg+"/"+basename — see PackagePath.File), never independently
// derived or canonicalized — there is no XTest-suffixed shape for this
// to strip, since a file's own address never carries that distinction.
// path.Dir, not filepath.Dir: an address's separator is always "/",
// regardless of the host OS.
func (f FilePath) PackagePath() PackagePath {
	return PackagePath(path.Dir(string(f)))
}

// RelativePath derives f's on-disk path relative to the workspace root
// — the one representation disk I/O needs (go/packages overlays,
// os.ReadFile/WriteFile). The disk boundary's own door out of FilePath;
// nothing past that boundary should need it.
func (f FilePath) RelativePath(module PackagePath) string {
	rel, _ := strings.CutPrefix(string(f), string(module)+"/")
	return rel
}

func (f FilePath) String() string { return string(f) }

// newFile builds a File from already-parsed content — the one construction
// point behind File's two legitimate doors, Workspace.SwapFile and
// Package.LoadFile, so a future field never has to be kept in sync by
// hand between them.
func newFile(path FilePath, src []byte, astFile *ast.File, dirty bool) *File {
	return &File{Path: path, src: src, ast: astFile, dirty: dirty}
}

// NewFilePath narrows raw, an untrusted agent-supplied file address,
// against module and pkg: a bare *.go name is accepted outright and
// joined onto pkg; a path is accepted only when its directory agrees
// with pkg, then narrowed to pkg plus its bare name. pkg is always the
// canonical, kind-stripped form — PackageID.Base() when the caller holds
// a resolved identity — since a file's address never carries the XTest
// distinction. Contradictions are refused, never guessed.
func NewFilePath(module, pkg PackagePath, raw string) (FilePath, error) {
	name := raw
	if strings.Contains(raw, "/") {
		rel, ok := cleanRelative(module, raw)
		if !ok {
			return "", fmt.Errorf("invalid file path %q", raw)
		}
		declaredPkg, err := NewPackageID(module, filepath.Dir(rel))
		if err != nil || declaredPkg.Base() != pkg {
			return "", fmt.Errorf("file %q does not live in package %q", raw, pkg)
		}
		name = filepath.Base(rel)
	}
	if !strings.HasSuffix(name, ".go") {
		return "", fmt.Errorf("file name must be a bare *.go name, got %q", name)
	}
	return pkg.File(name), nil
}

// cleanRelative narrows s, an untrusted string, against module: absolute
// paths and paths escaping the workspace root are refused; a spelling
// already prefixed by module resolves to its workspace-relative
// remainder, and the bare module root resolves to ".". The shared
// narrowing step behind NewPackageID and NewFilePath.
func cleanRelative(module PackagePath, s string) (string, bool) {
	p := filepath.Clean(s)
	if filepath.IsAbs(p) || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", false
	}
	if p == string(module) {
		return ".", true
	}
	if trimmed := strings.TrimPrefix(p, string(module)+"/"); trimmed != p {
		return trimmed, true
	}
	return p, true
}

// IsOutsideRoot reports whether a workspace-relative path (already
// cleaned) points outside the workspace root.
func IsOutsideRoot(p string) bool {
	return p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator))
}
