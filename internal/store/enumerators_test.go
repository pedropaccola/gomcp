package store

import "testing"

func TestViewMethods(t *testing.T) {
	v := viewFixture(t, "package pkg\n\ntype Box struct{}\n\nfunc (b Box) M() {}\n\nfunc Other() {}\n")
	if !v.HasPackage(tpkgPath("pkg")) {
		t.Fatal("test.mod/pkg not found")
	}
	methods := v.Methods(tpkgPath("pkg"), "Box")
	if len(methods) != 1 || methods[0].Key != "Box.M" {
		t.Errorf("Methods(Box) = %+v, want only Box.M", methods)
	}
}

func TestViewPackages(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	pkgs := v.Packages()
	if len(pkgs) != 1 || pkgs[0].Base() != tpkgPath("pkg") {
		t.Errorf("Packages() = %+v, want just test.mod/pkg", pkgs)
	}
}
