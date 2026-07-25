package gate

import (
	"strings"
	"testing"
)

func TestTxMoveSymbolRenamesInPlace(t *testing.T) {
	v := gateTypesFixture(t, map[string]string{
		"pkg": "package pkg\n\nfunc Foo() {}\n\nfunc UseFoo() { Foo() }\n",
	})
	tx := NewTx(v)
	if err := tx.MoveSymbol("test.mod/pkg", "Foo", "", "", "Bar"); err != nil {
		t.Fatalf("MoveSymbol rename: %v", err)
	}
	if _, _, ok := v.Symbol("test.mod/pkg", "Foo"); ok {
		t.Error("Foo must be gone after renaming to Bar")
	}
	if _, _, ok := v.Symbol("test.mod/pkg", "Bar"); !ok {
		t.Error("Bar must exist after renaming Foo")
	}
	src, ok := v.DeclSource("test.mod/pkg", "UseFoo")
	if !ok || !strings.Contains(src, "Bar()") {
		t.Errorf("UseFoo's call site = %q, want it repointed to Bar()", src)
	}
}

func TestTxMoveFileWithinPackage(t *testing.T) {
	v := gateFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.MoveFile("test.mod/pkg", "pkg.go", "", "renamed.go"); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	sym, _, ok := v.Symbol("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("Foo must survive the file rename")
	}
	if sym.File() != "pkg/renamed.go" {
		t.Errorf("Foo.File() = %q, want pkg/renamed.go", sym.File())
	}
}

func TestTxMovePackage(t *testing.T) {
	v := gateFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.MovePackage("test.mod/pkg", "test.mod/moved"); err != nil {
		t.Fatalf("MovePackage: %v", err)
	}
	if _, ok := v.Package("test.mod/pkg"); ok {
		t.Error("test.mod/pkg must be gone after MovePackage")
	}
	if _, ok := v.Package("test.mod/moved"); !ok {
		t.Error("test.mod/moved must exist after MovePackage")
	}
}
