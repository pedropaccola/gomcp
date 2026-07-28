package store

import "testing"

func TestTxCreateFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateFile("test.mod/pkg", "extra.go", ""); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	pkg, ok := v.Package("test.mod/pkg")
	if !ok {
		t.Fatal("test.mod/pkg not found")
	}
	if len(pkg.Files) != 2 {
		t.Errorf("pkg.Files = %+v, want the original file plus extra.go", pkg.Files)
	}
}

func TestTxCreateFileRefusesExisting(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateFile("test.mod/pkg", "pkg.go", ""); err == nil {
		t.Error("CreateFile must refuse a file that already exists")
	}
}

func TestTxCreatePackage(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	tx := NewTx(v)
	if err := tx.CreatePackage("test.mod/newpkg", ""); err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	pkg, ok := v.Package("test.mod/newpkg")
	if !ok {
		t.Fatal("newpkg not found after CreatePackage")
	}
	if len(pkg.Files) != 1 {
		t.Errorf("newpkg.Files = %+v, want exactly one seeded file", pkg.Files)
	}
}

func TestTxCreatePackageRefusesExisting(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	tx := NewTx(v)
	if err := tx.CreatePackage("test.mod/pkg", ""); err == nil {
		t.Error("CreatePackage must refuse an address that already holds a package")
	}
}

func TestTxCreateSymbolRefusesCollision(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateSymbol("test.mod/pkg", "pkg.go", "func Foo() {}"); err == nil {
		t.Error("CreateSymbol must refuse a name already declared in the package")
	}
}

func TestTxCreateSymbolTouchesFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateSymbol("test.mod/pkg", "pkg.go", "func Bar() {}"); err != nil {
		t.Fatalf("CreateSymbol: %v", err)
	}
	if _, _, ok := v.Symbol("test.mod/pkg", "Bar"); !ok {
		t.Error("Bar not found after CreateSymbol")
	}
	changed := tx.ChangedKeys()
	if len(changed) != 1 || changed[0] != "test.mod/pkg/pkg.go" {
		t.Errorf("ChangedKeys() = %v, want [test.mod/pkg/pkg.go]", changed)
	}
}
