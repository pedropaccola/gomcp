package gate

import (
	"strings"
	"testing"
)

func TestTxMoveSymbolRenamesInPlace(t *testing.T) {
	v := gateTypesFixture(t, map[string]string{
		"pkg": "package pkg\n\nfunc Foo() {}\n\nfunc UseFoo() { Foo() }\n",
	})
	tx := NewTx(v)
	if err := tx.MoveSymbol("test.mod/pkg", "Foo", "", "", "Bar"); err != nil {
		t.Fatalf("MoveSymbol rename: %v", err)
	}
	if _, _, ok := v.Symbol("test.mod/pkg", "Foo"); ok {
		t.Error("Foo must be gone after renaming to Bar")
	}
	if _, _, ok := v.Symbol("test.mod/pkg", "Bar"); !ok {
		t.Error("Bar must exist after renaming Foo")
	}
	src, ok := v.DeclSource("test.mod/pkg", "UseFoo")
	if !ok || !strings.Contains(src, "Bar()") {
		t.Errorf("UseFoo's call site = %q, want it repointed to Bar()", src)
	}
}

func TestTxMoveFileWithinPackage(t *testing.T) {
	v := gateFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.MoveFile("test.mod/pkg", "pkg.go", "", "renamed.go"); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	sym, _, ok := v.Symbol("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("Foo must survive the file rename")
	}
	if sym.File() != "pkg/renamed.go" {
		t.Errorf("Foo.File() = %q, want pkg/renamed.go", sym.File())
	}
}

func TestTxMovePackage(t *testing.T) {
	v := gateFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.MovePackage("test.mod/pkg", "test.mod/moved"); err != nil {
		t.Fatalf("MovePackage: %v", err)
	}
	if _, ok := v.Package("test.mod/pkg"); ok {
		t.Error("test.mod/pkg must be gone after MovePackage")
	}
	if _, ok := v.Package("test.mod/moved"); !ok {
		t.Error("test.mod/moved must exist after MovePackage")
	}
}

func TestTxMoveSymbolGroupMovesTypeAndMethods(t *testing.T) {
	view := gateTypesFixture(t, map[string]string{
		"src":  "package src\n\ntype Stack struct{}\n\nfunc (s Stack) Push() {}\n\nfunc (s Stack) Pop() {}\n",
		"dest": "package dest\n",
	})
	tx := NewTx(view)
	if err := tx.MoveSymbolGroup("test.mod/src", []string{"Stack", "Stack.Push", "Stack.Pop"}, "test.mod/dest", "stack.go"); err != nil {
		t.Fatalf("MoveSymbolGroup: %v", err)
	}
	if _, _, ok := tx.Symbol("test.mod/src", "Stack"); ok {
		t.Error("Stack should no longer exist in src")
	}
	for _, key := range []string{"Stack", "Stack.Push", "Stack.Pop"} {
		if _, _, ok := tx.Symbol("test.mod/dest", key); !ok {
			t.Errorf("%q missing from dest after MoveSymbolGroup", key)
		}
	}
}

func TestTxMoveSymbolGroupRefusesMethodsWithoutReceiver(t *testing.T) {
	view := gateTypesFixture(t, map[string]string{
		"src":  "package src\n\ntype Stack struct{}\n\nfunc (s Stack) Push() {}\n\nfunc (s Stack) Pop() {}\n",
		"dest": "package dest\n",
	})
	tx := NewTx(view)
	err := tx.MoveSymbolGroup("test.mod/src", []string{"Stack.Push", "Stack.Pop"}, "test.mod/dest", "stack.go")
	if err == nil || !strings.Contains(err.Error(), "receiver") {
		t.Errorf("MoveSymbolGroup(Push, Pop without Stack) = %v, want a receiver-must-move-too refusal", err)
	}
}

func TestTxMoveSymbolGroupRefusesSingleKey(t *testing.T) {
	view := gateTypesFixture(t, map[string]string{
		"src":  "package src\n\ntype Stack struct{}\n",
		"dest": "package dest\n",
	})
	tx := NewTx(view)
	err := tx.MoveSymbolGroup("test.mod/src", []string{"Stack"}, "test.mod/dest", "stack.go")
	if err == nil || !strings.Contains(err.Error(), "at least two") {
		t.Errorf("MoveSymbolGroup(single key) = %v, want a refusal pointing at refactor_move_symbol's single-key path", err)
	}
}

// TestTxMoveSymbolGroupCollapsesSameGroupToOneExtraction proves the fix:
// a batch naming two members of the same position-dependent const group
// (A and B, not C) must not error trying to re-resolve B after A's own
// ExtractDecl already pulled the whole group's text — and the unnamed
// sibling C must still move along, since the group can't be split.
func TestTxMoveSymbolGroupCollapsesSameGroupToOneExtraction(t *testing.T) {
	view := gateTypesFixture(t, map[string]string{
		"src":  "package src\n\nconst (\n\tA = iota\n\tB\n\tC\n)\n",
		"dest": "package dest\n",
	})
	tx := NewTx(view)
	if err := tx.MoveSymbolGroup("test.mod/src", []string{"A", "B"}, "test.mod/dest", "consts.go"); err != nil {
		t.Fatalf("MoveSymbolGroup: %v", err)
	}
	for _, key := range []string{"A", "B", "C"} {
		if _, _, ok := tx.Symbol("test.mod/src", key); ok {
			t.Errorf("%q should no longer exist in src", key)
		}
		if _, _, ok := tx.Symbol("test.mod/dest", key); !ok {
			t.Errorf("%q missing from dest after MoveSymbolGroup", key)
		}
	}
}
