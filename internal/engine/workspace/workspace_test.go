package workspace

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
)

func TestSwapFileParseEnforcedAndDirty(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[address.PkgPath]*Unit{})
	p := &Package{Name: "pkg", Path: "pkg", PkgPath: "example.com/mod/pkg"}
	if err := w.SwapFile(p, "pkg/pkg.go", "pkg/pkg.go", []byte("package pkg\n\nfunc broken( {}\n")); err == nil {
		t.Fatal("SwapFile accepted unparseable bytes")
	}
	if len(p.Files()) != 0 {
		t.Fatal("failed swap must leave the package untouched")
	}
	if err := w.SwapFile(p, "pkg/pkg.go", "pkg/pkg.go", []byte("package pkg\n\nfunc Hello() {}\n")); err != nil {
		t.Fatalf("SwapFile: %v", err)
	}
	file, ok := p.File("pkg/pkg.go")
	if !ok || !file.Dirty() {
		t.Fatal("swapped file missing or not dirty")
	}
	if _, ok := p.Symbol("Hello"); !ok {
		t.Error("index not rebuilt by SwapFile")
	}
	file.MarkFlushed()
	if file.Dirty() {
		t.Error("MarkFlushed did not clear the dirty mark")
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
	p.AddLoadedFile("example.com/dep/dep.go", src, astFile)
	file, ok := p.File("example.com/dep/dep.go")
	if !ok || file.Dirty() {
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

func TestCloneAndCloneShell(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[address.PkgPath]*Unit{})
	p := &Package{Name: "pkg", Path: "pkg", PkgPath: "example.com/mod/pkg"}
	if err := w.SwapFile(p, "pkg/pkg.go", "pkg/pkg.go", []byte("package pkg\n\nfunc Hello() {}\n")); err != nil {
		t.Fatal(err)
	}
	cloned := p.Clone()
	clonedFile, _ := cloned.File("pkg/pkg.go")
	origFile, _ := p.File("pkg/pkg.go")
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

func TestUnitDirtyCarryAndPrune(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[address.PkgPath]*Unit{})
	p := &Package{Name: "pkg", Path: "pkg", PkgPath: "example.com/mod/pkg"}
	if err := w.SwapFile(p, "pkg/pkg.go", "pkg/pkg.go", []byte("package pkg\n\nfunc Hello() {}\n")); err != nil {
		t.Fatal(err)
	}
	file, _ := p.File("pkg/pkg.go")
	file.MarkFlushed()
	unit := &Unit{Prod: p}
	unit.MarkDirty("pkg/pkg.go")
	if !file.Dirty() {
		t.Error("MarkDirty did not re-mark the carried-over file")
	}
	units := map[address.PkgPath]*Unit{"example.com/mod/pkg": unit}
	PruneFile(units, "example.com/mod/pkg", "pkg/pkg.go")
	if _, ok := units["example.com/mod/pkg"]; ok {
		t.Error("unit must be pruned once its last file is gone")
	}
}
