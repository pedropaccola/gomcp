package store

import (
	"testing"

	"github.com/pedropaccola/gomcp/internal/dto"
)

func TestViewPackage(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	pkg, ok := v.Package("test.mod/pkg")
	if !ok {
		t.Fatal("Package(test.mod/pkg) not found")
	}
	if pkg.Path != "test.mod/pkg" {
		t.Errorf("pkg.Path = %q, want test.mod/pkg", pkg.Path)
	}
	if _, ok := pkg.Symbol("Foo"); !ok {
		t.Errorf("pkg.Symbol(Foo) not found in %+v", pkg)
	}
}

func TestViewPackageNotFound(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	if _, ok := v.Package("test.mod/nosuch"); ok {
		t.Error("Package(test.mod/nosuch) = ok, want not found")
	}
}

func TestViewSymbol(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	sym, pkg, ok := v.Symbol("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("Symbol(Foo) not found")
	}
	if sym.Key != "Foo" || sym.Kind != dto.KindFunc {
		t.Errorf("Symbol(Foo) = %+v, want key Foo kind Func", sym)
	}
	if pkg.Path != "test.mod/pkg" {
		t.Errorf("owning pkg = %+v, want test.mod/pkg", pkg)
	}
}

func TestViewSymbolNotFound(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	if _, _, ok := v.Symbol("test.mod/pkg", "Missing"); ok {
		t.Error("Symbol(Missing) = ok, want not found")
	}
}

func TestViewModule(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	if got := v.Module(); got != "test.mod" {
		t.Errorf("Module() = %q, want test.mod", got)
	}
}
