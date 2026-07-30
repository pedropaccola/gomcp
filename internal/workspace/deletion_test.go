package workspace

import (
	"strings"
	"testing"
)

func TestDeletionSplicesSolo(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n\nfunc Bar() {}\n")
	splices, found, err := w.ComputeDeletionSplices("test.mod/pkg", "Foo")
	if err != nil || !found {
		t.Fatalf("DeletionSplices(Foo) = %v, %v, %v", splices, found, err)
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	out := splices.Apply(file.Src())
	if strings.Contains(string(out), "Foo") {
		t.Errorf("Foo survived deletion: %s", out)
	}
	if !strings.Contains(string(out), "func Bar() {}") {
		t.Errorf("Bar destroyed by deleting Foo: %s", out)
	}
}

func TestDeletionSplicesNotFound(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	_, found, err := w.ComputeDeletionSplices("test.mod/pkg", "NoSuch")
	if err != nil || found {
		t.Errorf("DeletionSplices(NoSuch) = found=%v, err=%v, want a not-found noop", found, err)
	}
}

func TestDeletionSplicesTrimsMultiNameSpec(t *testing.T) {
	// var a, b int — deleting a must trim it from the spec, not take b down.
	w := simpleFixture(t, "package pkg\n\nvar a, b int\n")
	splices, found, err := w.ComputeDeletionSplices("test.mod/pkg", "a")
	if err != nil || !found {
		t.Fatalf("DeletionSplices(a) = %v, %v, %v", splices, found, err)
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	out := string(splices.Apply(file.Src()))
	if !strings.Contains(out, "var b int") {
		t.Errorf("a not trimmed to a standalone var b: %s", out)
	}
}
