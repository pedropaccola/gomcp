package gate

import (
	"regexp"
	"testing"
)

func TestViewSymbolsLike(t *testing.T) {
	v := gateFixture(t, "package pkg\n\nfunc FooBar() {}\n\nfunc Baz() {}\n")
	matches := v.SymbolsLike("Foo")
	if len(matches) != 1 || matches[0].Symbol.Key != "FooBar" {
		t.Errorf("SymbolsLike(Foo) = %+v, want a single match on FooBar", matches)
	}
}

func TestViewSymbolsRegexp(t *testing.T) {
	v := gateFixture(t, "package pkg\n\nfunc FooBar() {}\n\nfunc Baz() {}\n")
	matches := v.SymbolsRegexp(regexp.MustCompile("func Baz"))
	if len(matches) != 1 || matches[0].Symbol.Key != "Baz" {
		t.Errorf("SymbolsRegexp(func Baz) = %+v, want only Baz", matches)
	}
}

func TestViewSymbolsImplementing(t *testing.T) {
	v := gateTypesFixture(t, map[string]string{
		"shapes": "package shapes\n\ntype Shape interface { Area() float64 }\n\ntype Circle struct{}\n\nfunc (c Circle) Area() float64 { return 1 }\n\ntype Square struct{}\n",
	})
	matches, err := v.SymbolsImplementing("test.mod/shapes", "Shape")
	if err != nil {
		t.Fatalf("SymbolsImplementing: %v", err)
	}
	if len(matches) != 1 || matches[0].Symbol.Key != "Circle" {
		t.Errorf("SymbolsImplementing(Shape) = %+v, want only Circle", matches)
	}
}

func TestViewSymbolsReferencing(t *testing.T) {
	v := gateTypesFixture(t, map[string]string{
		"pkg": "package pkg\n\nfunc Foo() int { return 1 }\n\nfunc Bar() int { return Foo() }\n",
	})
	matches, err := v.SymbolsReferencing("test.mod/pkg", "Foo")
	if err != nil {
		t.Fatalf("SymbolsReferencing: %v", err)
	}
	if len(matches) != 1 || matches[0].Symbol.Key != "Bar" {
		t.Errorf("SymbolsReferencing(Foo) = %+v, want only Bar", matches)
	}
}
