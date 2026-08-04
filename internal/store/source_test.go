package store

import (
	"strings"
	"testing"
)

func TestViewDeclSource(t *testing.T) {
	v := viewFixture(t, "package pkg\n\n// Foo does something.\nfunc Foo() {}\n")
	src, ok := v.DeclSource("test.mod/pkg", "Foo", "")
	if !ok || !strings.Contains(src, "Foo does something") {
		t.Errorf("DeclSource(Foo) = %q, ok=%v, want the doc comment included", src, ok)
	}
}

func TestViewSpecSource(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nconst (\n\tA = 1\n\tB = 2\n)\n")
	src, ok := v.SpecSource("test.mod/pkg", "A")
	if !ok || src != "A = 1" {
		t.Errorf("SpecSource(A) = %q, ok=%v, want just its own spec", src, ok)
	}
}

func TestViewSignature(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() int {\n\treturn 1\n}\n")
	sig, ok := v.Signature("test.mod/pkg", "Foo")
	if !ok || sig != "func Foo() int" {
		t.Errorf("Signature(Foo) = %q, ok=%v, want just the header", sig, ok)
	}
}
