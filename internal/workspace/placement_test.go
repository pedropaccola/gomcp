package workspace

import (
	"go/token"
	"strings"
	"testing"
)

func TestInsertOffsetAppendsFuncAtEnd(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	at, ok := w.InsertOffset("test.mod/pkg", "test.mod/pkg/pkg.go", KindFunc, "")
	if !ok {
		t.Fatal("InsertOffset must find a position for a new func")
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	src := string(file.Src())
	fooEnd := strings.Index(src, "func Foo() {}") + len("func Foo() {}")
	if at != fooEnd {
		t.Errorf("InsertOffset = %d, want right after Foo's decl (%d):\n%s", at, fooEnd, src)
	}
}

func TestInsertOffsetMethodNearReceiver(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\ntype Box struct{}\n\nfunc (b Box) M() {}\n\nfunc Other() {}\n")
	at, ok := w.InsertOffset("test.mod/pkg", "test.mod/pkg/pkg.go", KindMethod, "Box")
	if !ok {
		t.Fatal("InsertOffset must find a position for a new Box method")
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	src := string(file.Src())
	mEnd := strings.Index(src, "func (b Box) M() {}") + len("func (b Box) M() {}")
	otherStart := strings.Index(src, "func Other")
	if at < mEnd || at > otherStart {
		t.Errorf("InsertOffset(Box method) = %d, want between M's end (%d) and Other (%d):\n%s", at, mEnd, otherStart, src)
	}
}

func TestTypeDeclOffset(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\ntype Status int\n\nfunc Foo() {}\n")
	at, ok := w.TypeDeclOffset("test.mod/pkg", "test.mod/pkg/pkg.go", "Status")
	if !ok {
		t.Fatal("TypeDeclOffset must find Status")
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	src := string(file.Src())
	typeEnd := strings.Index(src, "type Status int") + len("type Status int")
	fooStart := strings.Index(src, "func Foo")
	if at < typeEnd || at > fooStart {
		t.Errorf("TypeDeclOffset(Status) = %d, want between Status's end (%d) and Foo (%d):\n%s", at, typeEnd, fooStart, src)
	}
}

func TestMergeableGroupInsertOffset(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nconst (\n\tA = 1\n\tB = 2\n)\n")
	at, ok := w.MergeableGroupInsertOffset("test.mod/pkg", "test.mod/pkg/pkg.go", token.CONST)
	if !ok {
		t.Fatal("expected a mergeable const group")
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	src := string(file.Src())
	bEnd := strings.Index(src, "B = 2") + len("B = 2")
	closeParen := strings.LastIndex(src, ")")
	if at < bEnd || at > closeParen {
		t.Errorf("MergeableGroupInsertOffset = %d, want inside the group after B (%d..%d):\n%s", at, bEnd, closeParen, src)
	}
}

func TestMergeableGroupInsertOffsetNoneForPositionDependent(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nconst (\n\tA = iota\n\tB\n)\n")
	if _, ok := w.MergeableGroupInsertOffset("test.mod/pkg", "test.mod/pkg/pkg.go", token.CONST); ok {
		t.Error("an iota group must never be reported mergeable")
	}
}

// TestInsertOffsetMethodAfterTypeWithNoExistingMethods covers the gap
// found while validating the placement heuristic: a type's first method
// in a file (no sibling methods yet to anchor to) must still land right
// after the type's own declaration, not fall to the bottom past an
// unrelated plain func.
func TestInsertOffsetMethodAfterTypeWithNoExistingMethods(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\ntype Box struct{}\n\nfunc Other() {}\n")
	at, ok := w.InsertOffset("test.mod/pkg", "test.mod/pkg/pkg.go", KindMethod, "Box")
	if !ok {
		t.Fatal("InsertOffset must find a position for Box's first method")
	}
	file, _, _ := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	src := string(file.Src())
	boxEnd := strings.Index(src, "type Box struct{}") + len("type Box struct{}")
	if at != boxEnd {
		t.Errorf("InsertOffset(Box method, no existing methods) = %d, want right after Box's type decl (%d):\n%s", at, boxEnd, src)
	}
}
