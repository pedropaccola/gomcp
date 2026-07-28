package store

import (
	"strings"
	"testing"
)

func TestTxEditFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.EditFile("test.mod/pkg", "pkg.go", "Package pkg does things."); err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	pkg, ok := v.Package("test.mod/pkg")
	if !ok {
		t.Fatal("test.mod/pkg not found")
	}
	if !strings.Contains(pkg.Doc, "does things") {
		t.Errorf("Package.Doc = %q, want the new file doc", pkg.Doc)
	}
}

func TestTxEditSymbol(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() int { return 1 }\n")
	tx := NewTx(v)
	if err := tx.EditSymbol("test.mod/pkg", "Foo", "func Foo() int { return 2 }"); err != nil {
		t.Fatalf("EditSymbol: %v", err)
	}
	src, ok := v.DeclSource("test.mod/pkg", "Foo")
	if !ok || !strings.Contains(src, "return 2") {
		t.Errorf("DeclSource(Foo) = %q, ok=%v, want updated body", src, ok)
	}
}

func TestTxEditSymbolRefusesUnknown(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.EditSymbol("test.mod/pkg", "Missing", "func Missing() {}"); err == nil {
		t.Error("EditSymbol must refuse a symbol that doesn't exist")
	}
}
