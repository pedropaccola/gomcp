package gate

import "testing"

func TestViewPackages(t *testing.T) {
	v := gateFixture(t, "package pkg\n\nfunc Foo() {}\n")
	pkgs := v.Packages()
	if len(pkgs) != 1 || pkgs[0].Path != "test.mod/pkg" {
		t.Errorf("Packages() = %+v, want just test.mod/pkg", pkgs)
	}
}

func TestViewMethods(t *testing.T) {
	v := gateFixture(t, "package pkg\n\ntype Box struct{}\n\nfunc (b Box) M() {}\n\nfunc Other() {}\n")
	pkg, ok := v.Package("test.mod/pkg")
	if !ok {
		t.Fatal("test.mod/pkg not found")
	}
	methods := v.Methods(pkg, "Box")
	if len(methods) != 1 || methods[0].Key != "Box.M" {
		t.Errorf("Methods(Box) = %+v, want only Box.M", methods)
	}
}
