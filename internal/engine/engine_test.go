package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestBootstrapLiveRepo self-hosts on this repository — the one test kept
// off fixtures, as a smoke check that the engine models real-world code.
func TestBootstrapLiveRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("live-repo bootstrap loads real dependencies; skipped in -short")
	}
	e := NewEngine(moduleRoot(t), nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if e.ws.Module() != "github.com/pedropaccola/gomcp" {
		t.Errorf("Module = %q, module path not learned at bootstrap", e.ws.Module())
	}
	unit, ok := e.ws.Unit("github.com/pedropaccola/gomcp/internal/engine")
	if !ok || unit.Prod == nil {
		t.Fatal("internal/engine unit missing after bootstrap")
	}
	if unit.Prod.PkgPath != "github.com/pedropaccola/gomcp/internal/engine" {
		t.Errorf("unexpected PkgPath %q", unit.Prod.PkgPath)
	}
	if sym, ok := unit.Prod.Symbol("Engine.Bootstrap"); !ok || sym.Kind != KindMethod {
		t.Error(`Symbol("Engine.Bootstrap") missing or not a method`)
	}
}

func TestBootstrapSandbox(t *testing.T) {
	e := sandboxEngine(t)
	if e.ws.Module() != "example.com/sandbox" {
		t.Errorf("Module = %q, module path not learned at bootstrap", e.ws.Module())
	}
	unit, ok := e.ws.Unit(spkg("shapes"))
	if !ok || unit.Prod == nil {
		t.Fatal("shapes unit missing")
	}
	pkg := unit.Prod

	if pkg.Name != "shapes" || pkg.PkgPath != "example.com/sandbox/shapes" {
		t.Errorf("Prod = %q %q, synthesized variants not filtered?", pkg.Name, pkg.PkgPath)
	}
	// Widest-variant preference: the in-package test file folds into Prod.
	if _, ok := pkg.File(RelativePath("shapes/internal_test.go")); !ok {
		t.Error("internal_test.go not in Prod: widest variant was not preferred")
	}
	if _, ok := pkg.Symbol("TestAreaInternal"); !ok {
		t.Error("in-package test symbol not indexed")
	}

	// External test package lands in XTest with its own namespace, under
	// its production sibling's address.
	if unit.XTest == nil || unit.XTest.Name != "shapes_test" {
		t.Fatalf("XTest missing or misnamed: %+v", unit.XTest)
	}
	if unit.XTest.PkgPath != "example.com/sandbox/shapes_test" {
		t.Errorf("XTest.PkgPath = %q", unit.XTest.PkgPath)
	}
	if _, ok := unit.XTest.Symbol("TestAreaExternal"); !ok {
		t.Error("external test symbol not indexed")
	}

	// Generic receivers unwrap to the base type name.
	if sym, ok := pkg.Symbol("Stack.Push"); !ok || sym.Recv != "Stack" {
		t.Errorf(`Symbol("Stack.Push") = %+v, generic receiver not unwrapped`, sym)
	}
	// init functions are keyless, collected per file.
	groups, ok := pkg.File(RelativePath("shapes/groups.go"))
	if !ok || len(groups.Inits) != 1 {
		t.Errorf("groups.go Inits = %v, want exactly one", groups)
	}
	// Blank identifiers are not addressable.
	if _, ok := pkg.Symbol("_"); ok {
		t.Error("blank identifier was indexed")
	}

	for _, addr := range e.ws.UnitKeys() {
		u, _ := e.ws.Unit(addr)
		for _, p := range []*Package{u.Prod, u.XTest} {
			if p == nil {
				continue
			}
			for _, f := range p.Files() {
				if f.Path.EscapesRoot() {
					t.Errorf("%s: tracked file escapes workspace root", f.Path)
				}
				if len(f.Src()) == 0 {
					t.Errorf("%s: empty Src, canonical-bytes invariant broken", f.Path)
				}
				if f.Dirty() {
					t.Errorf("%s: dirty right after bootstrap", f.Path)
				}
			}
		}
	}
}

func TestBootstrapIsIdempotent(t *testing.T) {
	e := sandboxEngine(t)
	first := len(e.ws.UnitKeys())
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	if len(e.ws.UnitKeys()) != first {
		t.Errorf("package count changed across re-bootstrap: %d -> %d", first, len(e.ws.UnitKeys()))
	}
}

func TestCleanPath(t *testing.T) {
	if p, ok := CleanPath("./internal//engine/"); !ok || p != "internal/engine" {
		t.Errorf(`CleanPath("./internal//engine/") = %q, %v`, p, ok)
	}
	if p, ok := CleanPath("main.go"); !ok || p != "main.go" {
		t.Errorf(`CleanPath("main.go") = %q, %v`, p, ok)
	}
	for _, bad := range []string{"/etc/passwd", "..", "../secret.go", "a/../../b"} {
		if p, ok := CleanPath(bad); ok {
			t.Errorf("CleanPath(%q) = %q, accepted an address outside the workspace", bad, p)
		}
	}
}

func TestSplitPos(t *testing.T) {
	cases := []struct {
		pos       string
		file      string
		line, col int
		ok        bool
	}{
		{"", "", 0, 0, false},
		{"-", "", 0, 0, false},
		{"/a/b.go:12:3", "/a/b.go", 12, 3, true},
		{"/a/b.go:12", "/a/b.go", 12, 0, true},
		{"/a/b.go", "/a/b.go", 0, 0, true},
	}
	for _, c := range cases {
		file, line, col, ok := splitPos(c.pos)
		if file != c.file || line != c.line || col != c.col || ok != c.ok {
			t.Errorf("splitPos(%q) = (%q,%d,%d,%v), want (%q,%d,%d,%v)",
				c.pos, file, line, col, ok, c.file, c.line, c.col, c.ok)
		}
	}
}

func TestIngestErrorsOnBrokenFile(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("go.mod", "module example.com/broken\n\ngo 1.21\n")
	writeFile("main.go", "package main\n\nfunc main() {\n") // unclosed body

	e := NewEngine(dir, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap must not fail on diagnostics: %v", err)
	}
	unit, ok := e.ws.Unit("example.com/broken")
	if !ok || unit.Prod == nil {
		t.Fatal("broken package missing from state")
	}
	var diags []Diagnostic
	if f, ok := unit.Prod.File("main.go"); ok {
		diags = append(diags, f.Diags...)
		if !bytes.Contains(f.Src(), []byte("func main()")) {
			t.Error("broken file Src not captured")
		}
	}
	diags = append(diags, unit.Prod.Diags...)
	if len(diags) == 0 {
		t.Error("expected diagnostics for a file with a parse error, got none")
	}
}
