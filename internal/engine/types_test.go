package engine

import (
	"slices"
	"strings"
	"testing"
)

func TestTypesLoadedAndTypeDiagnostics(t *testing.T) {
	e := sandboxEngine(t)
	err := e.Read(func(v *View) error {
		pkg, ok := v.Package(spkg("shapes"))
		if !ok || pkg.Types == nil || pkg.TypesInfo == nil {
			t.Fatal("shapes package missing type information after bootstrap")
		}
		var typeDiags []Diagnostic
		for _, d := range v.Diagnostics(spkg("broken")) {
			if d.Kind == DiagType {
				typeDiags = append(typeDiags, d)
			}
		}
		if len(typeDiags) == 0 {
			t.Error("broken package produced no DiagType diagnostics")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSymbolsImplementing(t *testing.T) {
	e := sandboxEngine(t)
	err := e.Read(func(v *View) error {
		shape, _, ok := v.Symbol(spkg("shapes"), "Shape")
		if !ok {
			t.Fatal("Shape interface not indexed")
		}
		matches, err := v.SymbolsImplementing(shape)
		if err != nil {
			t.Fatalf("SymbolsImplementing(Shape): %v", err)
		}
		keys := matchKeys(matches)
		for _, want := range []string{"shapes:Circle", "shapes:Square", "shapes:Base", "shapes:Embedded"} {
			if !slices.Contains(keys, want) {
				t.Errorf("implementors of Shape missing %s: %v", want, keys)
			}
		}
		if slices.Contains(keys, "shapes:NotShape") {
			t.Error("NotShape reported as a Shape implementor")
		}

		named, _, _ := v.Symbol(spkg("shapes"), "Named")
		if ms, err := v.SymbolsImplementing(named); err != nil || len(ms) != 0 {
			t.Errorf("implementors of Named = %v, %v; want none", matchKeys(ms), err)
		}

		circle, _, _ := v.Symbol(spkg("shapes"), "Circle")
		if _, err := v.SymbolsImplementing(circle); err == nil || !strings.Contains(err.Error(), "interface") {
			t.Errorf("SymbolsImplementing on a struct must explain it needs an interface, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSymbolsReferencing(t *testing.T) {
	e := sandboxEngine(t)
	err := e.Read(func(v *View) error {
		refsOf := func(pkg PkgPath, key string) []string {
			t.Helper()
			sym, _, ok := v.Symbol(pkg, key)
			if !ok {
				t.Fatalf("symbol %s:%s not indexed", pkg, key)
			}
			matches, err := v.SymbolsReferencing(sym)
			if err != nil {
				t.Fatalf("SymbolsReferencing(%s): %v", key, err)
			}
			return matchKeys(matches)
		}

		circleRefs := refsOf(spkg("shapes"), "Circle")
		for _, want := range []string{"use:c", "use:NewCircle"} {
			if !slices.Contains(circleRefs, want) {
				t.Errorf("references of Circle missing %s: %v", want, circleRefs)
			}
		}
		if slices.Contains(circleRefs, "use:TotalArea") {
			t.Errorf("TotalArea references Shape, not Circle: %v", circleRefs)
		}

		if refs := refsOf(spkg("shapes"), "Circle.Area"); !slices.Contains(refs, "use:UseArea") {
			t.Errorf("references of Circle.Area missing use:UseArea: %v", refs)
		}
		if refs := refsOf(spkg("shapes"), "Shape"); !slices.Contains(refs, "use:TotalArea") {
			t.Errorf("references of Shape missing use:TotalArea: %v", refs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
