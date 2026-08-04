package store

import (
	"slices"
	"strings"
	"testing"
)

func TestTxEditFile(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	doc := "Package pkg does things."
	if err := tx.EditFile("test.mod/pkg", "pkg.go", &doc, nil); err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	got, ok := v.PackageDoc(tpkgPath("pkg"))
	if !ok {
		t.Fatal("test.mod/pkg not found")
	}
	if !strings.Contains(got, "does things") {
		t.Errorf("PackageDoc = %q, want the new file doc", got)
	}
}

func TestTxEditSymbol(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() int { return 1 }\n")
	tx := NewTx(v)
	if err := tx.EditSymbol("test.mod/pkg", "Foo", "func Foo() int { return 2 }", ""); err != nil {
		t.Fatalf("EditSymbol: %v", err)
	}
	src, ok := v.DeclSource("test.mod/pkg", "Foo", "")
	if !ok || !strings.Contains(src, "return 2") {
		t.Errorf("DeclSource(Foo) = %q, ok=%v, want updated body", src, ok)
	}
}

func TestTxEditSymbolRefusesUnknown(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.EditSymbol("test.mod/pkg", "Missing", "func Missing() {}", ""); err == nil {
		t.Error("EditSymbol must refuse a symbol that doesn't exist")
	}
}

func TestTxEditFileDirectivesIndependentOfDoc(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n")
	tx := NewTx(v)
	if err := tx.CreateFile(tpkgPath("pkg"), false, "gen.go", "Original doc.", []string{"go:build linux"}); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	// doc changes, directives untouched (nil means "leave as-is").
	newDoc := "Updated doc."
	if err := tx.EditFile("test.mod/pkg", "gen.go", &newDoc, nil); err != nil {
		t.Fatalf("EditFile(doc only): %v", err)
	}
	file, _, ok := v.ws.ResolveFileByPath("test.mod/pkg/gen.go")
	if !ok {
		t.Fatal("gen.go not found")
	}
	if file.Doc() != "Updated doc." {
		t.Errorf("Doc() = %q, want %q", file.Doc(), "Updated doc.")
	}
	if !slices.Equal(file.Directives, []string{"go:build linux"}) {
		t.Errorf("Directives = %v, want untouched [go:build linux]", file.Directives)
	}

	// directives change, doc untouched (nil means "leave as-is").
	if err := tx.EditFile("test.mod/pkg", "gen.go", nil, []string{"go:build darwin"}); err != nil {
		t.Fatalf("EditFile(directives only): %v", err)
	}
	file, _, ok = v.ws.ResolveFileByPath("test.mod/pkg/gen.go")
	if !ok {
		t.Fatal("gen.go not found")
	}
	if file.Doc() != "Updated doc." {
		t.Errorf("Doc() = %q, directives-only edit must not disturb it", file.Doc())
	}
	if !slices.Equal(file.Directives, []string{"go:build darwin"}) {
		t.Errorf("Directives = %v, want [go:build darwin]", file.Directives)
	}

	// non-nil empty directives clears the block.
	if err := tx.EditFile("test.mod/pkg", "gen.go", nil, []string{}); err != nil {
		t.Fatalf("EditFile(clear directives): %v", err)
	}
	file, _, ok = v.ws.ResolveFileByPath("test.mod/pkg/gen.go")
	if !ok {
		t.Fatal("gen.go not found")
	}
	if len(file.Directives) != 0 {
		t.Errorf("Directives = %v, want cleared", file.Directives)
	}
	if file.Doc() != "Updated doc." {
		t.Errorf("Doc() = %q, clearing directives must not disturb it", file.Doc())
	}
}
