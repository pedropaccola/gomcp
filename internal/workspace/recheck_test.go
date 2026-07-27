package workspace

import (
	"go/token"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
)

func TestRecheckScopeIsTransitive(t *testing.T) {
	w := NewWorkspace()
	w.Reset("test.mod", token.NewFileSet(), map[address.PkgPath]*Unit{})
	for _, name := range []string{"a", "b", "c", "d"} {
		w.InstallUnit(address.PkgPath("test.mod/"+name), NewUnit(&Package{Name: name, Path: address.RelativePath(name), PkgPath: address.PkgPath("test.mod/" + name)}, nil))
	}
	mustSwap := func(pkg address.PkgPath, dir, src string) {
		t.Helper()
		if err := w.SwapFile(pkg, false, address.RelativePath(dir+"/"+dir+".go"), dir+".go", []byte(src)); err != nil {
			t.Fatalf("SwapFile(%s): %v", pkg, err)
		}
	}
	mustSwap("test.mod/a", "a", "package a\n")
	mustSwap("test.mod/b", "b", "package b\n\nimport \"test.mod/a\"\n\nvar _ = a.X\n")
	mustSwap("test.mod/c", "c", "package c\n\nimport \"test.mod/b\"\n\nvar _ = b.Y\n")
	mustSwap("test.mod/d", "d", "package d\n") // unrelated

	scope := w.ComputeRecheckScope(map[address.PkgPath]bool{"test.mod/a": true})
	for _, want := range []address.PkgPath{"test.mod/a", "test.mod/b", "test.mod/c"} {
		if !scope[want] {
			t.Errorf("scope missing %q, want transitive closure through b->c, got %v", want, scope)
		}
	}
	if scope["test.mod/d"] {
		t.Errorf("scope includes unrelated %q: %v", address.PkgPath("test.mod/d"), scope)
	}
}

func TestRecheckScopeIgnoresExternalImports(t *testing.T) {
	w := NewWorkspace()
	w.Reset("test.mod", token.NewFileSet(), map[address.PkgPath]*Unit{})
	w.InstallUnit("test.mod/a", NewUnit(&Package{Name: "a", Path: "a", PkgPath: "test.mod/a"}, nil))
	if err := w.SwapFile("test.mod/a", false, "a/a.go", "a.go", []byte("package a\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n")); err != nil {
		t.Fatalf("SwapFile: %v", err)
	}

	scope := w.ComputeRecheckScope(map[address.PkgPath]bool{"test.mod/a": true})
	if len(scope) != 1 || !scope["test.mod/a"] {
		t.Errorf("scope = %v, want exactly {test.mod/a}", scope)
	}
}
