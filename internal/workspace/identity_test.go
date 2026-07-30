package workspace

import "testing"

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
