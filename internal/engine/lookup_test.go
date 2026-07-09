package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

func TestLookupNavigation(t *testing.T) {
	e := sandboxEngine(t)
	err := e.Read(func(v *View) error {
		pkgs := v.Packages()
		var paths []RelativePath
		for _, p := range pkgs {
			paths = append(paths, p.Path)
		}
		if !slices.IsSorted(paths) {
			t.Error("Packages not in path order")
		}
		if !slices.Contains(paths, RelativePath("shapes")) {
			t.Fatalf("Packages missing shapes: %v", paths)
		}

		pkg, ok := v.Package(spkg("shapes"))
		if !ok || pkg.Name != "shapes" {
			t.Fatalf("Package(shapes) = %v, %v", pkg, ok)
		}
		xtest, ok := v.XTest(spkg("shapes"))
		if !ok || xtest.Name != "shapes_test" {
			t.Fatalf("XTest(shapes) = %v, %v", xtest, ok)
		}

		file, owner, ok := v.File("shapes/shapes.go")
		if !ok || file.Path != RelativePath("shapes/shapes.go") || owner != pkg {
			t.Fatal("File(shapes/shapes.go) resolution failed")
		}
		if _, xOwner, ok := v.File("shapes/external_test.go"); !ok || xOwner != xtest {
			t.Error("external test file must resolve to the XTest package")
		}
		if _, _, ok := v.File("does/not/exist.go"); ok {
			t.Error("File on a missing path must be comma-ok false")
		}

		// Package addresses are canonical at the engine level — spelling
		// tolerance lives in the tools gate (canonPkg). File paths still
		// clean, since they arrive from compiler positions too.
		if _, ok := v.Package("shapes"); ok {
			t.Error("bare directory must not resolve at the engine level")
		}
		if _, _, ok := v.File("./shapes/shapes.go"); !ok {
			t.Error("File must accept a ./ prefix")
		}

		// Symbol resolution falls through Prod into XTest.
		if sym, symOwner, ok := v.Symbol(spkg("shapes"), "TestAreaExternal"); !ok || symOwner != xtest || sym.Kind != KindFunc {
			t.Error("XTest-only symbol must resolve through the unit")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLookupSymbolsAndExtraction(t *testing.T) {
	e := sandboxEngine(t)
	err := e.Read(func(v *View) error {
		shape, owner, ok := v.Symbol(spkg("shapes"), "Shape")
		if !ok {
			t.Fatal(`Symbol(shapes, "Shape") not found`)
		}
		src, ok := v.DeclSource(shape)
		if !ok || !bytes.HasPrefix(src, []byte("// Shape is anything")) {
			t.Errorf("DeclSource must start at the doc comment, got %q", src)
		}
		if shape.Doc() == "" {
			t.Error("Doc() empty for a documented type")
		}

		area, _, _ := v.Symbol(spkg("shapes"), "Circle.Area")
		sig, ok := v.Signature(area)
		if !ok || string(sig) != "func (c Circle) Area() float64" {
			t.Errorf("Signature = %q", sig)
		}
		if _, ok := v.Signature(shape); ok {
			t.Error("Signature on a non-func symbol must be comma-ok false")
		}

		// Grouped members extract their own spec, doc included.
		kindCircle, _, ok := v.Symbol(spkg("shapes"), "KindCircle")
		if !ok {
			t.Fatal("grouped const not found")
		}
		spec, ok := v.SpecSource(kindCircle)
		if !ok || !bytes.HasPrefix(spec, []byte("// KindCircle is the round one.")) {
			t.Errorf("SpecSource(KindCircle) = %q, %v", spec, ok)
		}

		// Enumerators: methods on a generic receiver, files round-trip.
		if methods := v.Methods(owner, "Stack"); len(methods) != 1 || methods[0].Key() != "Stack.Push" {
			t.Errorf("Methods(Stack) = %v", methods)
		}
		for _, f := range v.Files(owner) {
			if _, o, ok := v.File(f.Path); !ok || o != owner {
				t.Errorf("Files entry %s does not resolve back to its package", f.Path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSymbolDiagnostics(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("go.mod", "module example.com/broken\n\ngo 1.21\n")
	writeFile("main.go", "package main\n\nfunc ok() {}\n\nfunc broken() {\n\tx :=\n}\n\nfunc main() {}\n")

	e := NewEngine(dir, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	err := e.Read(func(v *View) error {
		brokenSym, _, ok := v.Symbol("example.com/broken", "broken")
		if !ok {
			t.Skip("parser recovery did not index the broken decl; nothing to attribute")
		}
		if diags := v.SymbolDiagnostics(brokenSym); len(diags) == 0 {
			t.Error("SymbolDiagnostics(broken) empty, expected the parse error inside its span")
		}
		okSym, _, ok := v.Symbol("example.com/broken", "ok")
		if !ok {
			t.Fatal("healthy symbol not indexed")
		}
		if diags := v.SymbolDiagnostics(okSym); len(diags) != 0 {
			t.Errorf("SymbolDiagnostics(ok) = %v, want none: the view must not leak neighbors' problems", diags)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLookupScans(t *testing.T) {
	e := sandboxEngine(t)
	err := e.Read(func(v *View) error {
		hasKey := func(ms []Match, key string) bool {
			return slices.ContainsFunc(ms, func(m Match) bool { return m.Sym.Key() == key })
		}

		if ms := v.SymbolsLike("AREA"); !hasKey(ms, "Circle.Area") || !hasKey(ms, "TotalArea") {
			t.Error("SymbolsLike must match case-insensitively across Prod and use")
		}
		if ms := v.SymbolsLike("areaexternal"); !hasKey(ms, "TestAreaExternal") {
			t.Error("SymbolsLike must scan XTest packages too")
		}

		consts := v.SymbolsWhere(func(_ *Package, s *Symbol) bool { return s.Kind == KindConst })
		if !hasKey(consts, "KindCircle") {
			t.Error("SymbolsWhere(KindConst) missing KindCircle")
		}

		hits := v.SymbolsRegexp(regexp.MustCompile(`(?m)^type Embedded struct`))
		if len(hits) != 1 || hits[0].Sym.Key() != "Embedded" {
			t.Fatalf("SymbolsRegexp(type Embedded) = %+v, want the single symbol Embedded", hits)
		}

		// Content matching reaches inside bodies; grouped members attribute
		// to the one owning spec; hits dedupe per symbol.
		body := v.SymbolsRegexp(regexp.MustCompile(`append\(s\.items`))
		if len(body) != 1 || body[0].Sym.Key() != "Stack.Push" {
			t.Errorf("SymbolsRegexp(append) = %v", matchKeys(body))
		}
		grouped := v.SymbolsRegexp(regexp.MustCompile(`DefaultScale stretches`))
		if len(grouped) != 1 || grouped[0].Sym.Key() != "DefaultScale" {
			t.Errorf("grouped hit misattributed: %v", matchKeys(grouped))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
