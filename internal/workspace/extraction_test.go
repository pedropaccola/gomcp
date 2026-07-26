package workspace

import (
	"slices"
	"strings"
	"testing"
)

func TestExtractDecl(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\n// Foo does foo.\nfunc Foo() {}\n\nfunc Bar() {}\n")
	extracted, splice, err := w.ExtractDecl("test.mod/pkg", "Foo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extracted, "// Foo does foo.") || !strings.Contains(extracted, "func Foo() {}") {
		t.Errorf("ExtractDecl = %q, want doc comment and declaration", extracted)
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "pkg/pkg.go")
	src := file.Src()
	remaining := string(src[:splice.Start]) + string(src[splice.End:])
	if strings.Contains(remaining, "Foo") {
		t.Errorf("splice left Foo behind: %q", remaining)
	}
	if !strings.Contains(remaining, "func Bar() {}") {
		t.Errorf("splice removed more than Foo: %q", remaining)
	}
}

func TestExtractDeclGroupedMember(t *testing.T) {
	// Extracting one member of a plain (non-position-dependent) group takes
	// only that member's own spec, not the whole group.
	w := simpleFixture(t, "package pkg\n\nconst (\n\t// A is first.\n\tA = 1\n\tB = 2\n)\n")
	extracted, splice, err := w.ExtractDecl("test.mod/pkg", "A")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extracted, "// A is first.") || !strings.Contains(extracted, "A = 1") || strings.Contains(extracted, "B") {
		t.Errorf("ExtractDecl(A) = %q, want just A's own spec", extracted)
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "pkg/pkg.go")
	src := file.Src()
	remaining := string(src[:splice.Start]) + string(src[splice.End:])
	if strings.Contains(remaining, "A = 1") {
		t.Errorf("splice left A behind: %q", remaining)
	}
	if !strings.Contains(remaining, "B = 2") {
		t.Errorf("splice removed sibling B: %q", remaining)
	}
}

func TestPositionDependentGroupMembersExpandsIotaGroup(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src": "package src\n\nconst (\n\tBase = iota\n\tSibling\n)\n",
	})
	got, err := w.PositionDependentGroupMembers("src", "Base")
	if err != nil {
		t.Fatalf("PositionDependentGroupMembers: %v", err)
	}
	if len(got) != 2 || !slices.Contains(got, "Base") || !slices.Contains(got, "Sibling") {
		t.Errorf("PositionDependentGroupMembers(Base) = %v, want [Base Sibling]", got)
	}
}

func TestPositionDependentGroupMembersLeavesPlainGroupAlone(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src": "package src\n\nconst (\n\tBase = 1\n\tSibling = 2\n)\n",
	})
	got, err := w.PositionDependentGroupMembers("src", "Base")
	if err != nil {
		t.Fatalf("PositionDependentGroupMembers: %v", err)
	}
	if len(got) != 1 || got[0] != "Base" {
		t.Errorf("PositionDependentGroupMembers(Base) = %v, want just [Base]: a non-position-dependent group is grouped for readability only", got)
	}
}
