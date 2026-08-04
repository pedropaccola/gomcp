package testutil

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// funcImporter adapts a plain function to types.Importer, so TypesFixture
// can resolve cross-package imports among the fixture's own in-memory
// packages without a real module on disk.
type funcImporter func(path string) (*types.Package, error)

func (f funcImporter) Import(path string) (*types.Package, error) { return f(path) }

// SimpleFixture builds a single-package Workspace from src with no type
// information — the AST/index-only unit-test fixture for business rules
// (extraction, deletion, editing, placement, source, and
// substring/regexp scanning) that never touch go/types.
func SimpleFixture(tb testing.TB, src string) *workspace.Workspace {
	tb.Helper()
	w := workspace.NewWorkspace()
	w.Reset("test.mod", token.NewFileSet(), map[workspace.PackagePath]*workspace.Package{}, map[workspace.PackagePath]*workspace.Package{})
	w.InstallProd("test.mod/pkg", workspace.NewPackage("pkg", "test.mod/pkg", workspace.KindProd, nil, nil))
	if err := w.SwapFile("test.mod/pkg", workspace.KindProd, false, "test.mod/pkg/pkg.go", []byte(src)); err != nil {
		tb.Fatalf("SimpleFixture: SwapFile: %v", err)
	}
	return w
}

// TypesFixture builds a multi-package Workspace with real go/types
// information — the fixture for business rules that inspect type
// identity (DetectMoveConflicts, ComputeQualifierFixups,
// ComputeRenameSplices, ComputePackageMoveSplices, SymbolsImplementing,
// SymbolsReferencing). Each entry in srcs is one package's single file,
// keyed by its full import path — workspace callers use a bare path
// directly, store callers pre-qualify each key under "test.mod/" to
// match a real workspace address's module+directory shape; packages may
// reference each other by that same key, type-checked in dependency
// order as each is first imported, falling back to the standard importer
// for anything not in srcs.
func TypesFixture(tb testing.TB, srcs map[string]string) *workspace.Workspace {
	tb.Helper()
	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(srcs))
	for path, src := range srcs {
		f, err := parser.ParseFile(fset, path+"/file.go", src, parser.ParseComments)
		if err != nil {
			tb.Fatalf("TypesFixture: parse %s: %v", path, err)
		}
		files[path] = f
	}
	checked := make(map[string]*types.Package)
	infos := make(map[string]*types.Info)
	std := importer.Default()
	var doImport funcImporter
	doImport = func(path string) (*types.Package, error) {
		if pkg, ok := checked[path]; ok {
			return pkg, nil
		}
		f, ok := files[path]
		if !ok {
			return std.Import(path)
		}
		info := &types.Info{
			Defs: make(map[*ast.Ident]types.Object),
			Uses: make(map[*ast.Ident]types.Object),
		}
		cfg := &types.Config{Importer: doImport}
		pkg, err := cfg.Check(path, fset, []*ast.File{f}, info)
		if err != nil {
			tb.Fatalf("TypesFixture: type-check %s: %v", path, err)
		}
		checked[path] = pkg
		infos[path] = info
		return pkg, nil
	}

	w := workspace.NewWorkspace()
	w.Reset("test.mod", fset, map[workspace.PackagePath]*workspace.Package{}, map[workspace.PackagePath]*workspace.Package{})
	for path := range srcs {
		if _, err := doImport(path); err != nil {
			tb.Fatalf("TypesFixture: %v", err)
		}
	}
	for path, src := range srcs {
		name := files[path].Name.Name
		wp := workspace.NewPackage(name, workspace.PackagePath(path), workspace.KindProd, checked[path], infos[path])
		wp.LoadFile(workspace.FilePath(path+"/file.go"), []byte(src), files[path], false)
		wp.RebuildIndex()
		w.InstallProd(workspace.PackagePath(path), wp)
	}
	return w
}
