package workspace

import (
	"strings"
	"testing"
)

func TestDeclSourceUngrouped(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\n// Foo does something.\nfunc Foo() {}\n")
	got, ok := w.DeclSource("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("DeclSource must find Foo")
	}
	want := "// Foo does something.\nfunc Foo() {}"
	if got != want {
		t.Errorf("DeclSource(Foo) = %q, want %q", got, want)
	}
}

func TestDeclSourceGroupedReturnsWholeGroup(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\n// Values used elsewhere.\nconst (\n\tA = 1\n\tB = 2\n)\n")
	got, ok := w.DeclSource("test.mod/pkg", "A")
	if !ok {
		t.Fatal("DeclSource must find A")
	}
	if !strings.Contains(got, "A = 1") || !strings.Contains(got, "B = 2") {
		t.Errorf("DeclSource(A) = %q, want the whole group including sibling B", got)
	}
	if !strings.HasPrefix(got, "// Values used elsewhere.") {
		t.Errorf("DeclSource(A) = %q, want the group's doc comment included", got)
	}
}

func TestSpecSourceNarrowsToOwnSpec(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nconst (\n\tA = 1\n\tB = 2\n)\n")
	got, ok := w.SpecSource("test.mod/pkg", "A")
	if !ok {
		t.Fatal("SpecSource must find A")
	}
	if got != "A = 1" {
		t.Errorf("SpecSource(A) = %q, want just its own spec %q", got, "A = 1")
	}
}

func TestSpecSourceFallsBackToDeclSourceUngrouped(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\n// Foo does something.\nfunc Foo() {}\n")
	decl, ok := w.DeclSource("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("DeclSource must find Foo")
	}
	spec, ok := w.SpecSource("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("SpecSource must find Foo")
	}
	if spec != decl {
		t.Errorf("SpecSource(Foo) = %q, want it to fall back to DeclSource's %q", spec, decl)
	}
}

func TestSignatureFuncHeaderOnly(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\n// Foo does something.\nfunc Foo() int {\n\treturn 1\n}\n")
	got, ok := w.Signature("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("Signature must find Foo")
	}
	want := "func Foo() int"
	if got != want {
		t.Errorf("Signature(Foo) = %q, want %q", got, want)
	}
}

func TestSignatureFalseForNonFunc(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nconst A = 1\n")
	if _, ok := w.Signature("test.mod/pkg", "A"); ok {
		t.Error("Signature must be comma-ok false for a non-func symbol")
	}
}
