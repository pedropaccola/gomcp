package workspace

import (
	"context"
	"regexp"
	"testing"
)

func TestSymbolsLike(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc FooBar() {}\n\nfunc Baz() {}\n")
	matches := w.SymbolsLike(context.Background(), "Foo")
	if len(matches) != 1 || matches[0].Key != "FooBar" {
		t.Errorf("SymbolsLike(Foo) = %+v, want a single match on FooBar", matches)
	}
}

func TestSymbolsRegexp(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc FooBar() {}\n\nfunc Baz() {}\n")
	matches := w.SymbolsRegexp(context.Background(), regexp.MustCompile(`func Baz`))
	got := map[string]bool{}
	for _, m := range matches {
		got[m.Key] = true
	}
	if !got["Baz"] || got["FooBar"] || len(matches) != 1 {
		t.Errorf("SymbolsRegexp(func Baz) = %+v, want only Baz", matches)
	}
}

func TestAddressAtLineUngrouped(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n\nfunc Bar() {}\n")
	_, key, ok := w.AddressAtLine("test.mod/pkg/pkg.go", 3)
	if !ok || key != "Foo" {
		t.Errorf("AddressAtLine(3) = %q, %v, want Foo, true", key, ok)
	}
	_, key, ok = w.AddressAtLine("test.mod/pkg/pkg.go", 5)
	if !ok || key != "Bar" {
		t.Errorf("AddressAtLine(5) = %q, %v, want Bar, true", key, ok)
	}
}

func TestAddressAtLineGroupedPrefersOwnSpec(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nconst (\n\tA = 1\n\tB = 2\n)\n")
	_, key, ok := w.AddressAtLine("test.mod/pkg/pkg.go", 4)
	if !ok || key != "A" {
		t.Errorf("AddressAtLine(4) = %q, %v, want A, true", key, ok)
	}
	_, key, ok = w.AddressAtLine("test.mod/pkg/pkg.go", 5)
	if !ok || key != "B" {
		t.Errorf("AddressAtLine(5) = %q, %v, want B, true", key, ok)
	}
}

func TestAddressAtLineNotFound(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	if _, _, ok := w.AddressAtLine("test.mod/pkg/pkg.go", 1); ok {
		t.Error("AddressAtLine on the package line must find no enclosing declaration")
	}
}

func TestSymbolsImplementing(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"shapes": "package shapes\n\ntype Shape interface { Area() float64 }\n\ntype Circle struct{}\n\nfunc (c Circle) Area() float64 { return 1 }\n\ntype Square struct{}\n",
	})
	matches, err := w.SymbolsImplementing(context.Background(), "shapes", "Shape")
	if err != nil {
		t.Fatalf("SymbolsImplementing: %v", err)
	}
	if len(matches) != 1 || matches[0].Key != "Circle" {
		t.Errorf("SymbolsImplementing(Shape) = %+v, want only Circle", matches)
	}
}

func TestSymbolsImplementingRejectsNonInterface(t *testing.T) {
	w := typesFixture(t, map[string]string{"shapes": "package shapes\n\ntype Circle struct{}\n"})
	if _, err := w.SymbolsImplementing(context.Background(), "shapes", "Circle"); err == nil {
		t.Error("SymbolsImplementing must error when key names a non-interface type")
	}
}

func TestSymbolsReferencing(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src": "package src\n\nfunc Foo() int { return 1 }\n\nfunc Bar() int { return Foo() }\n",
	})
	matches, err := w.SymbolsReferencing(context.Background(), "src", "Foo")
	if err != nil {
		t.Fatalf("SymbolsReferencing: %v", err)
	}
	if len(matches) != 1 || matches[0].Key != "Bar" {
		t.Errorf("SymbolsReferencing(Foo) = %+v, want only Bar", matches)
	}
}

func TestSymbolsReferencingExcludesSelfReference(t *testing.T) {
	w := typesFixture(t, map[string]string{"src": "package src\n\nfunc Recur() int { return Recur() }\n"})
	matches, err := w.SymbolsReferencing(context.Background(), "src", "Recur")
	if err != nil {
		t.Fatalf("SymbolsReferencing: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("SymbolsReferencing(Recur) = %+v, want no matches for pure self-recursion", matches)
	}
}
