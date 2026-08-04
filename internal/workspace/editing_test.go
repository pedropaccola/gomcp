package workspace

import (
	"go/token"
	"slices"
	"strings"
	"testing"
)

func TestEditPlanUngrouped(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	wasPositionDependent, groupTok, target, err := w.ComputeEditPlan("test.mod/pkg", "Foo", "")
	if err != nil {
		t.Fatal(err)
	}
	if wasPositionDependent {
		t.Error("ungrouped func must not be position-dependent")
	}
	if groupTok != token.ILLEGAL {
		t.Errorf("groupTok = %v, want ILLEGAL for an ungrouped decl", groupTok)
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	if got := string(file.Src()[target.Start:target.End]); !strings.Contains(got, "func Foo() {}") {
		t.Errorf("target = %q, want the whole Foo declaration", got)
	}
}

func TestEditPlanGroupedNonPositionDependent(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nconst (\n\tA = 1\n\tB = 2\n)\n")
	wasPositionDependent, groupTok, target, err := w.ComputeEditPlan("test.mod/pkg", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	if wasPositionDependent {
		t.Error("explicit-valued const group must not be position-dependent")
	}
	if groupTok != token.CONST {
		t.Errorf("groupTok = %v, want CONST", groupTok)
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	got := string(file.Src()[target.Start:target.End])
	if !strings.Contains(got, "A = 1") || strings.Contains(got, "B") {
		t.Errorf("target = %q, want just A's own spec", got)
	}
}

func TestEditPlanPositionDependent(t *testing.T) {
	// An iota group's target is the whole group: a single member can't be
	// replaced without breaking the positions of the rest.
	w := simpleFixture(t, "package pkg\n\nconst (\n\tA = iota\n\tB\n)\n")
	wasPositionDependent, groupTok, target, err := w.ComputeEditPlan("test.mod/pkg", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	if !wasPositionDependent {
		t.Error("iota group must be position-dependent")
	}
	if groupTok != token.CONST {
		t.Errorf("groupTok = %v, want CONST", groupTok)
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	got := string(file.Src()[target.Start:target.End])
	if !strings.Contains(got, "A = iota") || !strings.Contains(got, "B") {
		t.Errorf("target = %q, want the whole group (A and B)", got)
	}
}

func TestDetectEditCollisions(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n\nfunc Bar() {}\n")
	if got := w.DetectEditCollisions("test.mod/pkg", "Foo", []string{"Bar"}); !slices.Contains(got, "Bar") {
		t.Errorf("DetectEditCollisions(Foo, [Bar]) = %v, want it to name the collision", got)
	}
	if got := w.DetectEditCollisions("test.mod/pkg", "Foo", []string{"Baz"}); len(got) != 0 {
		t.Errorf("DetectEditCollisions(Foo, [Baz]) = %v, want none", got)
	}
	// A replacement is always allowed to keep declaring its own current name.
	if got := w.DetectEditCollisions("test.mod/pkg", "Foo", []string{"Foo"}); len(got) != 0 {
		t.Errorf("DetectEditCollisions(Foo, [Foo]) = %v, want the symbol's own name excused", got)
	}
}
