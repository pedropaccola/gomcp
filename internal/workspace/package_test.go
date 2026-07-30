package workspace

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestCloneAndCloneShell(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[PackagePath]*Unit{})
	w.InstallUnit("example.com/mod/pkg", NewUnit(&Package{Name: "pkg", ID: newPackageID("example.com/mod/pkg", KindProd)}, nil))
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
	shell := p.cloneShell()
	if shell.Name != p.Name || shell.ID != p.ID {
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
	p := &Package{Name: "dep", ID: newPackageID("example.com/dep", KindExternal)}
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

func TestNewPackageID(t *testing.T) {
	const module = PackagePath("test.mod")
	if id, err := NewPackageID(module, "./internal//store/"); err != nil || id.String() != "test.mod/internal/store" || id.Kind() != KindProd {
		t.Errorf(`NewPackageID(module, "./internal//store/") = %q, kind %v, %v`, id, id.Kind(), err)
	}
	if id, err := NewPackageID(module, "test.mod/internal/store"); err != nil || id.String() != "test.mod/internal/store" || id.Kind() != KindProd {
		t.Errorf(`NewPackageID(module, "test.mod/internal/store") = %q, kind %v, %v`, id, id.Kind(), err)
	}
	if id, err := NewPackageID(module, "test.mod/internal/store_test"); err != nil || id.String() != "test.mod/internal/store_test" || id.Kind() != KindXTest || id.Base() != "test.mod/internal/store" {
		t.Errorf(`NewPackageID(module, "test.mod/internal/store_test") = %q, kind %v, base %q, %v`, id, id.Kind(), id.Base(), err)
	}
	for _, bad := range []string{"/etc/passwd", "..", "../secret", "a/../../b", "main.go"} {
		if id, err := NewPackageID(module, bad); err == nil {
			t.Errorf("NewPackageID(module, %q) = %q, accepted an invalid package address", bad, id)
		}
	}
}

func TestPackageIDBaseNeverCarriesKind(t *testing.T) {
	const module = PackagePath("test.mod")
	prod, err := NewPackageID(module, "pkg")
	if err != nil {
		t.Fatal(err)
	}
	xtest, err := NewPackageID(module, "pkg_test")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Base() != xtest.Base() {
		t.Errorf("Prod and XTest halves of the same unit must share one canonical Base: %q vs %q", prod.Base(), xtest.Base())
	}
	if prod.String() == xtest.String() {
		t.Error("Prod and XTest full spellings must differ")
	}
}

func TestPackageKindString(t *testing.T) {
	for kind, want := range map[PackageKind]string{KindProd: "prod", KindXTest: "xtest", KindExternal: "external"} {
		if got := kind.String(); got != want {
			t.Errorf("PackageKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}
