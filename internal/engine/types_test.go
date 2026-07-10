package engine

import (
	"bytes"
	"context"
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

func TestExternalLoading(t *testing.T) {
	e := sandboxEngine(t)
	if err := e.LoadExternal(context.Background(), "io"); err != nil {
		t.Fatalf("LoadExternal(io): %v", err)
	}
	err := e.Read(func(v *View) error {
		pkg, ok := v.ExternalPackage("io")
		if !ok {
			t.Fatal("io missing from the external cache")
		}
		reader, ok := pkg.Symbol("Reader")
		if !ok {
			t.Fatal("io.Reader not indexed")
		}
		src, ok := v.DeclSource(reader)
		if !ok || !bytes.Contains(src, []byte("Read(p []byte) (n int, err error)")) {
			t.Errorf("DeclSource(io.Reader) = %q, %v", src, ok)
		}
		for _, sym := range pkg.Symbols() {
			if r := sym.Key()[0]; r >= 'a' && r <= 'z' {
				t.Errorf("unexported symbol %q leaked into the external index", sym.Key())
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExternalRefusalsAndReset(t *testing.T) {
	e := sandboxEngine(t)
	if err := e.LoadExternal(context.Background(), "no.such.host/bogus"); err == nil {
		t.Error("bogus import path must error")
	}
	if err := e.LoadExternal(context.Background(), "no.such.host/bogus"); err == nil {
		t.Error("negative cache must keep refusing")
	}
	if err := e.LoadExternal(context.Background(), "io"); err != nil {
		t.Fatalf("LoadExternal(io): %v", err)
	}
	// Dependencies are read-only: mutation verbs never see them.
	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.CreateSymbol("io", "extra.go", "func Nope() {}")
	}); err == nil || !strings.Contains(err.Error(), "no package") {
		t.Errorf("mutating a dependency must fail, got %v", err)
	}
	// The cache lives and dies with the workspace snapshot.
	if _, err := e.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	e.Read(func(v *View) error {
		if _, ok := v.ExternalPackage("io"); ok {
			t.Error("external cache survived reload")
		}
		return nil
	})
}
