package store

import (
	"testing"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

func TestViewPackage(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	if !v.HasPackage(tpkgPath("pkg")) {
		t.Fatal("Package(test.mod/pkg) not found")
	}
	if _, ok := v.Symbol("test.mod/pkg", "Foo"); !ok {
		t.Error("Symbol(Foo) not found")
	}
}

func TestViewPackageNotFound(t *testing.T) {
	v := viewFixture(t, "package pkg\n")
	if v.HasPackage(tpkgPath("nosuch")) {
		t.Error("Package(test.mod/nosuch) = ok, want not found")
	}
}

func TestViewSymbol(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	sym, ok := v.Symbol("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("Symbol(Foo) not found")
	}
	if sym.Key != "Foo" || sym.Kind != workspace.KindFunc.String() {
		t.Errorf("Symbol(Foo) = %+v, want key Foo kind Func", sym)
	}
	if sym.Owner.Base() != "test.mod/pkg" {
		t.Errorf("owning pkg = %+v, want test.mod/pkg", sym.Owner)
	}
}

func TestViewSymbolNotFound(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	if _, ok := v.Symbol("test.mod/pkg", "Missing"); ok {
		t.Error("Symbol(Missing) = ok, want not found")
	}
}

func TestViewModule(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	if got := v.Module(); got != "test.mod" {
		t.Errorf("Module() = %q, want test.mod", got)
	}
}
