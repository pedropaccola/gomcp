package workspace

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestCloneAndCloneShell(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[PackagePath]*Package{}, map[PackagePath]*Package{})
	w.InstallProd("example.com/mod/pkg", &Package{Name: "pkg", ID: newPackageID("example.com/mod/pkg", KindProd)})
	if err := w.SwapFile("example.com/mod/pkg", KindProd, false, "example.com/mod/pkg/pkg.go", []byte("package pkg\n\nfunc Hello() {}\n")); err != nil {
		t.Fatal(err)
	}
	p, _ := w.ProdPackage("example.com/mod/pkg")
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
	p.LoadFile("example.com/dep/dep.go", src, astFile, false)
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

func TestNewPackagePath(t *testing.T) {
	const module = PackagePath("test.mod")
	if got, err := NewPackagePath(module, "./internal//store/"); err != nil || got != "test.mod/internal/store" {
		t.Errorf(`NewPackagePath(module, "./internal//store/") = %q, %v`, got, err)
	}
	if got, err := NewPackagePath(module, "test.mod/internal/store"); err != nil || got != "test.mod/internal/store" {
		t.Errorf(`NewPackagePath(module, "test.mod/internal/store") = %q, %v`, got, err)
	}
	if got, err := NewPackagePath(module, "test.mod/internal/store_test"); err != nil || got != "test.mod/internal/store_test" {
		t.Errorf(`NewPackagePath(module, "test.mod/internal/store_test") = %q, %v, want the literal address unsplit — no suffix carries a kind anymore`, got, err)
	}
	for _, bad := range []string{"/etc/passwd", "..", "../secret", "a/../../b", "main.go"} {
		if got, err := NewPackagePath(module, bad); err == nil {
			t.Errorf("NewPackagePath(module, %q) = %q, accepted an invalid package address", bad, got)
		}
	}
}

func TestPackageKindString(t *testing.T) {
	for kind, want := range map[PackageKind]string{KindProd: "prod", KindXTest: "xtest", KindExternal: "external"} {
		if got := kind.String(); got != want {
			t.Errorf("PackageKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}
