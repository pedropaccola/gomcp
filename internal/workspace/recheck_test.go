package workspace

import (
	"go/token"
	"testing"
)

func TestRecheckScopeIsTransitive(t *testing.T) {
	w := NewWorkspace()
	w.Reset("test.mod", token.NewFileSet(), map[PackagePath]*Package{}, map[PackagePath]*Package{})
	for _, name := range []string{"a", "b", "c", "d"} {
		w.InstallProd(PackagePath("test.mod/"+name), &Package{Name: name, ID: newPackageID(PackagePath("test.mod/"+name), KindProd)})
	}
	mustSwap := func(pkg PackagePath, dir, src string) {
		t.Helper()
		if err := w.SwapFile(pkg, KindProd, false, FilePath(string(pkg)+"/"+dir+".go"), []byte(src)); err != nil {
			t.Fatalf("SwapFile(%s): %v", pkg, err)
		}
	}
	mustSwap("test.mod/a", "a", "package a\n")
	mustSwap("test.mod/b", "b", "package b\n\nimport \"test.mod/a\"\n\nvar _ = a.X\n")
	mustSwap("test.mod/c", "c", "package c\n\nimport \"test.mod/b\"\n\nvar _ = b.Y\n")
	mustSwap("test.mod/d", "d", "package d\n") // unrelated

	scope := w.ComputeRecheckScope(map[PackagePath]bool{"test.mod/a": true})
	for _, want := range []PackagePath{"test.mod/a", "test.mod/b", "test.mod/c"} {
		if !scope[want] {
			t.Errorf("scope missing %q, want transitive closure through b->c, got %v", want, scope)
		}
	}
	if scope["test.mod/d"] {
		t.Errorf("scope includes unrelated %q: %v", PackagePath("test.mod/d"), scope)
	}
}

func TestRecheckScopeIgnoresExternalImports(t *testing.T) {
	w := NewWorkspace()
	w.Reset("test.mod", token.NewFileSet(), map[PackagePath]*Package{}, map[PackagePath]*Package{})
	w.InstallProd("test.mod/a", &Package{Name: "a", ID: newPackageID("test.mod/a", KindProd)})
	if err := w.SwapFile("test.mod/a", KindProd, false, "a/a.go", []byte("package a\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n")); err != nil {
		t.Fatalf("SwapFile: %v", err)
	}

	scope := w.ComputeRecheckScope(map[PackagePath]bool{"test.mod/a": true})
	if len(scope) != 1 || !scope["test.mod/a"] {
		t.Errorf("scope = %v, want exactly {test.mod/a}", scope)
	}
}
