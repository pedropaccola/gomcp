package gate

import (
	"context"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// funcImporter adapts a plain function to types.Importer, so
// gateTypesFixture can resolve cross-package imports among the fixture's
// own in-memory packages without a real module on disk.
type funcImporter func(path string) (*types.Package, error)

func (f funcImporter) Import(path string) (*types.Package, error) { return f(path) }

// gateFixture builds a single-package Workspace with no type information,
// wrapped in a View backed by a real (empty) temp directory — the
// unit-test fixture for gate's read pass-throughs and Tx's pipeline
// mechanics (goimports formatting, SwapFile, touch-tracking), with no
// real go/packages.Load. tb.TempDir gives reloadFile's goimports pass a
// genuine, existing directory to resolve against without needing a real
// module on disk.
func gateFixture(tb testing.TB, src string) *View {
	tb.Helper()
	ws := workspace.NewWorkspace()
	ws.Reset("test.mod", token.NewFileSet(), map[address.PkgPath]*workspace.Unit{})
	ws.InstallUnit("test.mod/pkg", workspace.NewUnit(workspace.NewPackage("pkg", "pkg", "test.mod/pkg", nil, nil, false), nil))
	if err := ws.SwapFile("test.mod/pkg", false, "pkg/pkg.go", "pkg/pkg.go", []byte(src)); err != nil {
		tb.Fatalf("gateFixture: SwapFile: %v", err)
	}
	return NewView(tb.TempDir(), ws, context.Background())
}

// gateTypesFixture builds a multi-package Workspace with real go/types
// information, wrapped in a View backed by a real (empty) temp directory
// — the fixture for gate verbs whose underlying workspace analysis
// (MoveSymbol's rename, MoveFile's cross-package checks) needs real type
// identity, not just AST/index data. Mirrors workspace's own typesFixture
// exactly, since gate has no access to workspace's unexported test
// helpers from outside the package, with one addition: gate's write
// pipeline resolves a package's directory from its PkgPath by stripping
// the module prefix (View.dirOf), so each entry's key is a bare
// directory name installed under the "test.mod/" module — not a raw
// import path the way workspace's own typesFixture uses it — and a
// cross-package reference in fixture source must import the full
// "test.mod/<key>" path to resolve.
func gateTypesFixture(tb testing.TB, srcs map[string]string) *View {
	tb.Helper()
	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(srcs))
	for dir, src := range srcs {
		f, err := parser.ParseFile(fset, dir+"/file.go", src, parser.ParseComments)
		if err != nil {
			tb.Fatalf("gateTypesFixture: parse %s: %v", dir, err)
		}
		files[dir] = f
	}
	checked := make(map[string]*types.Package)
	infos := make(map[string]*types.Info)
	std := importer.Default()
	var doImport funcImporter
	doImport = func(path string) (*types.Package, error) {
		if pkg, ok := checked[path]; ok {
			return pkg, nil
		}
		dir, ok := strings.CutPrefix(path, "test.mod/")
		f, ok2 := files[dir]
		if !ok || !ok2 {
			return std.Import(path)
		}
		info := &types.Info{
			Defs: make(map[*ast.Ident]types.Object),
			Uses: make(map[*ast.Ident]types.Object),
		}
		cfg := &types.Config{Importer: doImport}
		pkg, err := cfg.Check(path, fset, []*ast.File{f}, info)
		if err != nil {
			tb.Fatalf("gateTypesFixture: type-check %s: %v", path, err)
		}
		checked[path] = pkg
		infos[path] = info
		return pkg, nil
	}

	ws := workspace.NewWorkspace()
	ws.Reset("test.mod", fset, map[address.PkgPath]*workspace.Unit{})
	for dir := range srcs {
		if _, err := doImport("test.mod/" + dir); err != nil {
			tb.Fatalf("gateTypesFixture: %v", err)
		}
	}
	for dir, src := range srcs {
		fullPath := "test.mod/" + dir
		name := files[dir].Name.Name
		wp := workspace.NewPackage(name, address.RelativePath(dir), address.PkgPath(fullPath), checked[fullPath], infos[fullPath], false)
		wp.AddLoadedFile(address.RelativePath(dir+"/file.go"), []byte(src), files[dir])
		wp.RebuildIndex()
		ws.InstallUnit(address.PkgPath(fullPath), workspace.NewUnit(wp, nil))
	}
	return NewView(tb.TempDir(), ws, context.Background())
}
