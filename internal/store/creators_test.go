package store

import "testing"

func TestTxCreateFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateFile(tpkgID("pkg"), "extra.go", ""); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	files, ok := v.PackageFiles(tpkgID("pkg"))
	if !ok {
		t.Fatal("test.mod/pkg not found")
	}
	if len(files) != 2 {
		t.Errorf("files = %+v, want the original file plus extra.go", files)
	}
}

func TestTxCreateFileRefusesExisting(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateFile(tpkgID("pkg"), "pkg.go", ""); err == nil {
		t.Error("CreateFile must refuse a file that already exists")
	}
}

func TestTxCreatePackage(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	tx := NewTx(v)
	if err := tx.CreatePackage("test.mod/newpkg", ""); err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	files, ok := v.PackageFiles(tpkgID("newpkg"))
	if !ok {
		t.Fatal("newpkg not found after CreatePackage")
	}
	if len(files) != 1 {
		t.Errorf("newpkg files = %+v, want exactly one seeded file", files)
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
	if err := tx.CreateSymbol(tpkgID("pkg"), "pkg.go", "func Foo() {}"); err == nil {
		t.Error("CreateSymbol must refuse a name already declared in the package")
	}
}

func TestTxCreateSymbolTouchesFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateSymbol(tpkgID("pkg"), "pkg.go", "func Bar() {}"); err != nil {
		t.Fatalf("CreateSymbol: %v", err)
	}
	if _, ok := v.Symbol("test.mod/pkg", "Bar"); !ok {
		t.Error("Bar not found after CreateSymbol")
	}
	changed := tx.ChangedKeys()
	if len(changed) != 1 || changed[0] != "test.mod/pkg/pkg.go" {
		t.Errorf("ChangedKeys() = %v, want [test.mod/pkg/pkg.go]", changed)
	}
}

func TestTxCreateFileXTest(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateFile(tpkgID("pkg_test"), "extra_test.go", ""); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	files, ok := v.PackageFiles(tpkgID("pkg"))
	if !ok {
		t.Fatal("test.mod/pkg not found")
	}
	if len(files) != 1 {
		t.Errorf("Prod files = %+v, want the original file alone", files)
	}
	unit, ok := v.ws.Unit("test.mod/pkg")
	if !ok {
		t.Fatal("test.mod/pkg unit not found")
	}
	xtest := unit.XTest()
	if xtest == nil {
		t.Fatal("XTest half not installed")
	}
	if xtest.Name != "pkg_test" || xtest.ID.String() != "test.mod/pkg_test" {
		t.Errorf("XTest = %+v, want Name pkg_test and ID test.mod/pkg_test", xtest)
	}
	if _, ok := xtest.File("test.mod/pkg/extra_test.go"); !ok {
		t.Error("extra_test.go not installed in the new XTest package")
	}
}

// TestTxCreateFileXTestOriginatesProd targets a brand-new package's
// XTest half directly, with no create_packages call first: EnsurePackage
// must originate a Prod shell (one seeded file, same as CreatePackage's
// own construction) alongside the requested XTest file, in one
// transaction — no separate create_packages round trip needed.
func TestTxCreateFileXTestOriginatesProd(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	tx := NewTx(v)
	if err := tx.CreateFile(tpkgID("missing_test"), "extra_test.go", ""); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	unit, ok := v.ws.Unit("test.mod/missing")
	if !ok {
		t.Fatal("test.mod/missing unit not found")
	}
	prod := unit.Prod()
	if prod == nil || prod.Name != "missing" {
		t.Fatalf("Prod half not originated: %+v", prod)
	}
	if _, ok := prod.File("test.mod/missing/missing.go"); !ok {
		t.Error("Prod half missing its seeded stub file")
	}
	xtest := unit.XTest()
	if xtest == nil || xtest.Name != "missing_test" {
		t.Fatalf("XTest half not installed: %+v", xtest)
	}
	if _, ok := xtest.File("test.mod/missing/extra_test.go"); !ok {
		t.Error("extra_test.go not installed in the new XTest package")
	}
}
