package engine

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/gate"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

func TestLookupNavigation(t *testing.T) {
	e := sandboxEngine(t)
	ws := e.ws

	var paths []address.RelativePath
	for _, addr := range ws.UnitKeys() {
		unit, _ := ws.Unit(addr)
		if prod := unit.Prod(); prod != nil {
			paths = append(paths, prod.Path)
		}
		if xtest := unit.XTest(); xtest != nil {
			paths = append(paths, xtest.Path)
		}
	}
	if !slices.IsSorted(paths) {
		t.Error("Packages not in path order")
	}
	if !slices.Contains(paths, address.RelativePath("shapes")) {
		t.Fatalf("Packages missing shapes: %v", paths)
	}

	unit, ok := ws.Unit(spkg("shapes"))
	if !ok {
		t.Fatal("no unit at shapes")
	}
	pkg := unit.Prod()
	if pkg == nil || pkg.Name != "shapes" {
		t.Fatalf("Package(shapes) = %v", pkg)
	}
	xtest := unit.XTest()
	if xtest == nil || xtest.Name != "shapes_test" {
		t.Fatalf("XTest(shapes) = %v", xtest)
	}

	file, ok := pkg.File("shapes/shapes.go")
	if !ok || file.Path != address.RelativePath("shapes/shapes.go") {
		t.Fatal("File(shapes/shapes.go) resolution failed")
	}
	if _, ok := xtest.File("shapes/external_test.go"); !ok {
		t.Error("external test file must resolve to the XTest package")
	}
	if _, ok := pkg.File("does/not/exist.go"); ok {
		t.Error("File on a missing path must be comma-ok false")
	}

	// Package addresses are canonical at the engine level — spelling
	// tolerance lives in the tools gate (canonicalizePkg).
	if _, ok := ws.Unit("shapes"); ok {
		t.Error("bare directory must not resolve at the engine level")
	}
	if p := address.RelativePath("./shapes/shapes.go").Clean(); p != "shapes/shapes.go" {
		t.Errorf("RelativePath.Clean must strip a leading ./ prefix, got %q", p)
	}

	// Symbol resolution falls through Prod into XTest.
	if sym, ok := xtest.Symbol("TestAreaExternal"); !ok || sym.Kind != workspace.KindFunc {
		t.Error("XTest-only symbol must resolve through the unit")
	}
}

func TestLookupSymbolsAndExtraction(t *testing.T) {
	e := sandboxEngine(t)
	err := e.Read(context.Background(), func(v *gate.View) error {
		owner, ok := v.Package(spkg("shapes"))
		if !ok {
			t.Fatal(`Package(shapes) not found`)
		}
		if _, _, ok := v.Symbol(spkg("shapes"), "Shape"); !ok {
			t.Fatal(`Symbol(shapes, "Shape") not found`)
		}
		src, ok := v.DeclSource(spkg("shapes"), "Shape")
		if !ok || !strings.HasPrefix(src, "// Shape is anything") {
			t.Errorf("DeclSource must start at the doc comment, got %q", src)
		}

		sig, ok := v.Signature(spkg("shapes"), "Circle.Area")
		if !ok || sig != "func (c Circle) Area() float64" {
			t.Errorf("Signature = %q", sig)
		}
		if _, ok := v.Signature(spkg("shapes"), "Shape"); ok {
			t.Error("Signature on a non-func symbol must be comma-ok false")
		}

		// Grouped members extract their own spec, doc included.
		spec, ok := v.SpecSource(spkg("shapes"), "KindCircle")
		if !ok || !strings.HasPrefix(spec, "// KindCircle is the round one.") {
			t.Errorf("SpecSource(KindCircle) = %q, %v", spec, ok)
		}

		// Enumerators: methods on a generic receiver, files round-trip.
		if methods := v.Methods(owner, "Stack"); len(methods) != 1 || methods[0].Key() != "Stack.Push" {
			t.Errorf("Methods(Stack) = %v", methods)
		}
		for _, f := range owner.Files() {
			if f.Path() == "" {
				t.Error("Files entry has empty path")
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
	err := e.Read(context.Background(), func(v *gate.View) error {
		if _, _, ok := v.Symbol("example.com/broken", "broken"); !ok {
			t.Skip("parser recovery did not index the broken decl; nothing to attribute")
		}
		if diags := v.SymbolDiagnostics("example.com/broken", "broken"); len(diags) == 0 {
			t.Error("SymbolDiagnostics(broken) empty, expected the parse error inside its span")
		}
		if _, _, ok := v.Symbol("example.com/broken", "ok"); !ok {
			t.Fatal("healthy symbol not indexed")
		}
		if diags := v.SymbolDiagnostics("example.com/broken", "ok"); len(diags) != 0 {
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
	err := e.Read(context.Background(), func(v *gate.View) error {
		hasKey := func(ms []dto.Match, key string) bool {
			return slices.ContainsFunc(ms, func(m dto.Match) bool { return m.Sym.Key() == key })
		}

		if ms := v.SymbolsLike("AREA"); !hasKey(ms, "Circle.Area") || !hasKey(ms, "TotalArea") {
			t.Error("SymbolsLike must match case-insensitively across Prod and use")
		}
		if ms := v.SymbolsLike("areaexternal"); !hasKey(ms, "TestAreaExternal") {
			t.Error("SymbolsLike must scan XTest packages too")
		}

		var consts []dto.Match
		for _, pkg := range v.Packages() {
			for _, sym := range pkg.Symbols() {
				if sym.Kind() == dto.KindConst {
					consts = append(consts, dto.Match{Pkg: pkg, Sym: sym})
				}
			}
		}
		if !hasKey(consts, "KindCircle") {
			t.Error("expected KindConst symbols to include KindCircle")
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

func TestTypesLoadedAndTypeDiagnostics(t *testing.T) {
	e := sandboxEngine(t)
	ws := e.ws
	unit, ok := ws.Unit(spkg("shapes"))
	if !ok || unit.Prod() == nil || unit.Prod().Types() == nil || unit.Prod().TypesInfo() == nil {
		t.Fatal("shapes package missing type information after bootstrap")
	}
	err := e.Read(context.Background(), func(v *gate.View) error {
		var typeDiags []dto.Diagnostic
		for _, d := range v.Diagnostics(spkg("broken")) {
			if d.Kind == dto.DiagType {
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
	err := e.Read(context.Background(), func(v *gate.View) error {
		matches, err := v.SymbolsImplementing(spkg("shapes"), "Shape")
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

		if ms, err := v.SymbolsImplementing(spkg("shapes"), "Named"); err != nil || len(ms) != 0 {
			t.Errorf("implementors of Named = %v, %v; want none", matchKeys(ms), err)
		}

		if _, err := v.SymbolsImplementing(spkg("shapes"), "Circle"); err == nil || !strings.Contains(err.Error(), "interface") {
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
	err := e.Read(context.Background(), func(v *gate.View) error {
		refsOf := func(pkg address.PkgPath, key string) []string {
			t.Helper()
			matches, err := v.SymbolsReferencing(pkg, key)
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

// TestPublicViewSurface exercises the DTO-returning public methods
// directly (Package, Symbol, Packages, ExternalPackage, DeclSource,
// SpecSource, Signature, SymbolDiagnostics).
func TestPublicViewSurface(t *testing.T) {
	e := sandboxEngine(t)
	if err := e.LoadExternal(context.Background(), "io"); err != nil {
		t.Fatalf("LoadExternal(io): %v", err)
	}
	err := e.Read(context.Background(), func(v *gate.View) error {
		pkg, ok := v.Package(spkg("shapes"))
		if !ok {
			t.Fatal("Package(shapes) not found")
		}
		if pkg.PkgPath() != spkg("shapes") {
			t.Errorf("Package.PkgPath() = %q, want %q", pkg.PkgPath(), spkg("shapes"))
		}
		if len(pkg.Files()) == 0 || len(pkg.Symbols()) == 0 {
			t.Error("Package.Files()/Symbols() empty: translator dropped data")
		}
		if _, ok := pkg.Symbol("Shape"); !ok {
			t.Error("Package.Symbol(Shape) not found on the translated package")
		}

		sym, owner, ok := v.Symbol(spkg("shapes"), "Circle.Area")
		if !ok {
			t.Fatal("Symbol(Circle.Area) not found")
		}
		if sym.Kind() != dto.KindMethod || sym.Recv() != "Circle" {
			t.Errorf("Symbol(Circle.Area) = %+v, translator lost kind/recv", sym)
		}
		if owner.PkgPath() != spkg("shapes") {
			t.Errorf("Symbol owner PkgPath() = %q", owner.PkgPath())
		}

		if sig, ok := v.Signature(spkg("shapes"), "Circle.Area"); !ok || sig != "func (c Circle) Area() float64" {
			t.Errorf("Signature(Circle.Area) = %q, %v", sig, ok)
		}
		if _, ok := v.DeclSource(spkg("shapes"), "Shape"); !ok {
			t.Error("DeclSource(Shape) failed through the public surface")
		}
		if _, ok := v.SpecSource(spkg("shapes"), "KindCircle"); !ok {
			t.Error("SpecSource(KindCircle) failed through the public surface")
		}
		if diags := v.SymbolDiagnostics(spkg("shapes"), "Shape"); len(diags) != 0 {
			t.Errorf("SymbolDiagnostics(Shape) = %v, want none for a healthy symbol", diags)
		}

		pkgs := v.Packages()
		if len(pkgs) == 0 {
			t.Fatal("Packages() empty")
		}
		for _, p := range pkgs {
			if p.PkgPath() == "" {
				t.Error("Packages() entry with empty PkgPath")
			}
		}

		extPkg, ok := v.ExternalPackage("io")
		if !ok {
			t.Fatal("ExternalPackage(io) not found")
		}
		if _, ok := extPkg.Symbol("Reader"); !ok {
			t.Error("ExternalPackage(io).Symbol(Reader) not found")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPackageAndFileDoc(t *testing.T) {
	e := sandboxEngine(t)
	e.Read(context.Background(), func(v *gate.View) error {
		pkg, ok := v.Package(spkg("shapes"))
		if !ok {
			t.Fatal("shapes package not resolvable")
		}
		want := "Kinds are grouped separately from shapes themselves.\n\nPackage shapes provides fixture shape types for tests."
		if pkg.Doc() != want {
			t.Errorf("Package.Doc() = %q, want %q", pkg.Doc(), want)
		}
		var groupsDoc, shapesDoc string
		for _, f := range pkg.Files() {
			switch f.Path().Base() {
			case "groups.go":
				groupsDoc = f.Doc()
			case "shapes.go":
				shapesDoc = f.Doc()
			}
		}
		if groupsDoc != "Kinds are grouped separately from shapes themselves." {
			t.Errorf("groups.go Doc() = %q", groupsDoc)
		}
		if shapesDoc != "Package shapes provides fixture shape types for tests." {
			t.Errorf("shapes.go Doc() = %q", shapesDoc)
		}

		use, ok := v.Package(spkg("use"))
		if !ok {
			t.Fatal("use package not resolvable")
		}
		if use.Doc() != "" {
			t.Errorf("use.Doc() = %q, want empty (no fixture doc comments)", use.Doc())
		}
		return nil
	})
}

// TestDiagnosticsPreservesPackageOnPositionlessProblem covers the case
// View.Diagnostics(pkg) already knows the package but attributeDiagnostics
// used to re-derive it from position alone: a package-level problem with
// no usable file position (go/packages' own "found packages X and Y in
// one directory" conflict is a real, reproducible trigger, not
// synthesized) must still come back attributed to pkg, not empty.
func TestDiagnosticsPreservesPackageOnPositionlessProblem(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("go.mod", "module example.com/conflict\n\ngo 1.21\n")
	writeFile("a.go", "package foo\n\nfunc A() {}\n")
	writeFile("b.go", "package bar\n\nfunc B() {}\n")

	e := NewEngine(dir, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	err := e.Read(context.Background(), func(v *gate.View) error {
		diags := v.Diagnostics("example.com/conflict")
		i := slices.IndexFunc(diags, func(d dto.Diagnostic) bool { return d.File == "" })
		if i < 0 {
			t.Fatal("expected a position-less diagnostic (go/packages' package-name-conflict error); none found")
		}
		if diags[i].Package != "example.com/conflict" {
			t.Errorf("Package = %q, want the caller's own known package %q", diags[i].Package, "example.com/conflict")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
