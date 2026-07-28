package workspace

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
)

func TestCloneAndCloneShell(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[address.PkgPath]*Unit{})
	w.InstallUnit("example.com/mod/pkg", NewUnit(&Package{Name: "pkg", PkgPath: "example.com/mod/pkg"}, nil))
	if err := w.SwapFile("example.com/mod/pkg", false, "example.com/mod/pkg/pkg.go", []byte("package pkg\n\nfunc Hello() {}\n")); err != nil {
		t.Fatal(err)
	}
	unit, _ := w.Unit("example.com/mod/pkg")
	p := unit.Prod()
	cloned := p.Clone()
	clonedFile, _ := cloned.File("example.com/mod/pkg/pkg.go")
	origFile, _ := p.File("example.com/mod/pkg/pkg.go")
	if clonedFile != origFile {
		t.Error("Clone must share File values")
	}
	shell := p.CloneShell()
	if shell.Name != p.Name || shell.PkgPath != p.PkgPath {
		t.Error("CloneShell must keep metadata")
	}
	if len(shell.Files()) != 0 || len(shell.Symbols()) != 0 {
		t.Error("CloneShell must start with no files and an empty index")
	}
}

func TestLoadPathAndExternalIndex(t *testing.T) {
	fset := token.NewFileSet()
	src := []byte("package dep\n\nfunc Exported() {}\n\nfunc hidden() {}\n")
	astFile, err := parser.ParseFile(fset, "dep.go", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	p := &Package{Name: "dep", PkgPath: "example.com/dep", External: true}
	p.LoadFile("example.com/dep/dep.go", src, astFile)
	file, ok := p.File("example.com/dep/dep.go")
	if !ok || file.IsDirty() {
		t.Fatal("loaded file missing or dirty — the load path must install clean files")
	}
	p.RebuildIndex()
	if _, ok := p.Symbol("Exported"); !ok {
		t.Error("exported symbol missing from the external index")
	}
	if _, ok := p.Symbol("hidden"); ok {
		t.Error("unexported symbol leaked into an external index")
	}
}
