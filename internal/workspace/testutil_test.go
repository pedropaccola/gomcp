package workspace

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"sort"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
)

// funcImporter adapts a plain function to types.Importer, so typesFixture
// can resolve cross-package imports among the fixture's own in-memory
// packages without a real module on disk.
type funcImporter func(path string) (*types.Package, error)

func (f funcImporter) Import(path string) (*types.Package, error) { return f(path) }

// simpleFixture builds a single-package Workspace from src with no type
// information — the AST/index-only unit-test fixture for business rules
// (extraction, deletion, editing, placement, source, and
// substring/regexp scanning) that never touch go/types.
func simpleFixture(tb testing.TB, src string) *Workspace {
	tb.Helper()
	w := NewWorkspace()
	w.Reset("test.mod", token.NewFileSet(), map[address.PkgPath]*Unit{})
	w.InstallUnit("test.mod/pkg", NewUnit(&Package{Name: "pkg", Path: "pkg", PkgPath: "test.mod/pkg"}, nil))
	if err := w.SwapFile("test.mod/pkg", false, "pkg/pkg.go", "pkg/pkg.go", []byte(src)); err != nil {
		tb.Fatalf("fixture SwapFile: %v", err)
	}
	return w
}

// applyTestSplices applies Workspace's own Splice value objects to src in
// descending offset order — the test-only mirror of gate's applySplices,
// since workspace only computes splices, never applies them itself.
func applyTestSplices(src []byte, splices []Splice) []byte {
	sort.Slice(splices, func(i, j int) bool { return splices[i].Start > splices[j].Start })
	out := slices.Clone(src)
	for _, s := range splices {
		out = slices.Concat(out[:s.Start], s.Repl, out[s.End:])
	}
	return out
}

// typesFixture builds a multi-package Workspace with real go/types
// information — the fixture for business rules that inspect type
// identity (DetectMoveConflicts, ComputeQualifierFixups,
// ComputeRenameSplices, ComputePackageMoveSplices, SymbolsImplementing,
// SymbolsReferencing). Each entry in srcs is one package's single file,
// keyed by its import path; packages may reference each other by that
// same import path, type-checked in dependency order as each is first
// imported, falling back to the standard importer for anything not in
// srcs.
func typesFixture(tb testing.TB, srcs map[string]string) *Workspace {
	tb.Helper()
	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(srcs))
	for path, src := range srcs {
		f, err := parser.ParseFile(fset, path+"/file.go", src, parser.ParseComments)
		if err != nil {
			tb.Fatalf("typesFixture: parse %s: %v", path, err)
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
			tb.Fatalf("typesFixture: type-check %s: %v", path, err)
		}
		checked[path] = pkg
		infos[path] = info
		return pkg, nil
	}

	w := NewWorkspace()
	w.Reset("test.mod", fset, map[address.PkgPath]*Unit{})
	for path := range srcs {
		if _, err := doImport(path); err != nil {
			tb.Fatalf("typesFixture: %v", err)
		}
	}
	for path, src := range srcs {
		name := files[path].Name.Name
		wp := NewPackage(name, address.RelativePath(name), address.PkgPath(path), checked[path], infos[path], false)
		wp.LoadFile(address.RelativePath(name+"/file.go"), []byte(src), files[path])
		wp.RebuildIndex()
		w.InstallUnit(address.PkgPath(path), NewUnit(wp, nil))
	}
	return w
}
