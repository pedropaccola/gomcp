package store

import (
	"slices"
	"testing"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

func TestTxCreateFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateFile(tpkgPath("pkg"), false, "extra.go", "", nil); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	files, ok := v.PackageFiles(tpkgPath("pkg"))
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
	if err := tx.CreateFile(tpkgPath("pkg"), false, "pkg.go", "", nil); err == nil {
		t.Error("CreateFile must refuse a file that already exists")
	}
}

func TestTxCreatePackage(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	tx := NewTx(v)
	if err := tx.CreatePackage("test.mod/newpkg", "", false); err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	files, ok := v.PackageFiles(tpkgPath("newpkg"))
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
	if err := tx.CreatePackage("test.mod/pkg", "", false); err == nil {
		t.Error("CreatePackage must refuse an address that already holds a package")
	}
}

func TestTxCreateSymbolRefusesCollision(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateSymbol(tpkgPath("pkg"), "pkg.go", "func Foo() {}"); err == nil {
		t.Error("CreateSymbol must refuse a name already declared in the package")
	}
}

func TestTxCreateSymbolTouchesFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateSymbol(tpkgPath("pkg"), "pkg.go", "func Bar() {}"); err != nil {
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
	if err := tx.CreatePackage("test.mod/pkg", "", true); err != nil {
		t.Fatalf("CreatePackage(isXTest): %v", err)
	}
	if err := tx.CreateFile("test.mod/pkg", true, "extra_test.go", "", nil); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	prod, ok := v.ws.ProdPackage("test.mod/pkg")
	if !ok || len(prod.Files()) != 1 {
		t.Errorf("Prod = %+v, want the original file alone, untouched by the XTest creation", prod)
	}
	var xtest *workspace.Package
	for _, p := range v.ws.MembersOf("test.mod/pkg") {
		if p.ID.Kind() == workspace.KindXTest {
			xtest = p
		}
	}
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

func TestTxCreateFileWithDirectives(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateFile(tpkgPath("pkg"), false, "gen.go", "Gen holds generated-shaped fixtures.", []string{"go:build linux", "go:generate mockgen -source=gen.go"}); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	file, _, ok := v.ws.ResolveFileByPath("test.mod/pkg/gen.go")
	if !ok {
		t.Fatal("gen.go not found")
	}
	want := []string{"go:build linux", "go:generate mockgen -source=gen.go"}
	if !slices.Equal(file.Directives, want) {
		t.Errorf("Directives = %v, want %v", file.Directives, want)
	}
	if file.Doc() != "Gen holds generated-shaped fixtures." {
		t.Errorf("Doc() = %q", file.Doc())
	}
}

// TestTxCreateFileFailsIfPackageMissing confirms CreateFile no longer
// implicitly originates a package or its XTest half — the target half
// must already exist via CreatePackage first, in its own separate step.
func TestTxCreateFileFailsIfPackageMissing(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	tx := NewTx(v)
	if err := tx.CreateFile("test.mod/missing", true, "extra_test.go", "", nil); err == nil {
		t.Error("CreateFile must fail when the target XTest half doesn't exist yet, not originate it implicitly")
	}
	if err := tx.CreateFile("test.mod/missing", false, "extra.go", "", nil); err == nil {
		t.Error("CreateFile must fail when the target Prod half doesn't exist yet")
	}
}

// TestTxCreatePackageXTestWithoutProd confirms the dirty-buffer principle
// applies to package creation too: an agent may write the test before
// the implementation, and CreatePackage must not assume the reverse
// order or fabricate a Prod sibling to paper over it.
func TestTxCreatePackageXTestWithoutProd(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	tx := NewTx(v)
	if err := tx.CreatePackage("test.mod/fresh", "", true); err != nil {
		t.Fatalf("CreatePackage(isXTest) with no Prod sibling: %v", err)
	}
	if _, ok := v.ws.ProdPackage("test.mod/fresh"); ok {
		t.Error("CreatePackage(isXTest) must not fabricate a Prod sibling")
	}
	var xtest *workspace.Package
	for _, p := range v.ws.MembersOf("test.mod/fresh") {
		if p.ID.Kind() == workspace.KindXTest {
			xtest = p
		}
	}
	if xtest == nil || xtest.Name != "fresh_test" {
		t.Fatalf("XTest half not installed: %+v", xtest)
	}
}
