package store

import "testing"

func TestTxDeleteFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.DeleteFile("test.mod/pkg", "pkg.go"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, ok := v.Symbol("test.mod/pkg", "Foo"); ok {
		t.Error("Foo must be gone after its file is deleted")
	}
}

func TestTxDeletePackage(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.DeletePackage("test.mod/pkg"); err != nil {
		t.Fatalf("DeletePackage: %v", err)
	}
	if v.HasPackage(tpkgPath("pkg")) {
		t.Error("test.mod/pkg must be gone after DeletePackage")
	}
}

func TestTxDeleteSymbol(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n\nfunc Bar() {}\n")
	tx := NewTx(v)
	if err := tx.DeleteSymbol("test.mod/pkg", "Foo", ""); err != nil {
		t.Fatalf("DeleteSymbol: %v", err)
	}
	if _, ok := v.Symbol("test.mod/pkg", "Foo"); ok {
		t.Error("Foo still resolves after DeleteSymbol")
	}
	if _, ok := v.Symbol("test.mod/pkg", "Bar"); !ok {
		t.Error("Bar must survive deleting its sibling Foo")
	}
}

func TestTxDeleteSymbolNoopIfAbsent(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.DeleteSymbol("test.mod/pkg", "Missing", ""); err != nil {
		t.Errorf("DeleteSymbol on an absent symbol must be a noop, got: %v", err)
	}
}
