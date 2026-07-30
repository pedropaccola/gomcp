package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// TestBootstrapLiveRepo self-hosts on this repository — the one test kept
// off fixtures, as a smoke check that the store models real-world code.
func TestBootstrapLiveRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("live-repo bootstrap loads real dependencies; skipped in -short")
	}
	e := NewStore(moduleRoot(t), nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if e.ws.Module() != "github.com/pedropaccola/gomcp" {
		t.Errorf("Module = %q, module path not learned at bootstrap", e.ws.Module())
	}
	unit, ok := e.ws.Unit("github.com/pedropaccola/gomcp/internal/store")
	prod := unit.Prod()
	if !ok || prod == nil {
		t.Fatal("internal/store unit missing after bootstrap")
	}
	if prod.ID.String() != "github.com/pedropaccola/gomcp/internal/store" {
		t.Errorf("unexpected ID %q", prod.ID)
	}
	if sym, ok := prod.Symbol("Store.Bootstrap"); !ok || sym.Kind != workspace.KindMethod {
		t.Error(`Symbol("Store.Bootstrap") missing or not a method`)
	}
}

func TestBootstrapSandbox(t *testing.T) {
	e := sandboxStore(t)
	if e.ws.Module() != "example.com/sandbox" {
		t.Errorf("Module = %q, module path not learned at bootstrap", e.ws.Module())
	}
	unit, ok := e.ws.Unit(spkg("shapes"))
	pkg := unit.Prod()
	if !ok || pkg == nil {
		t.Fatal("shapes unit missing")
	}

	if pkg.Name != "shapes" || pkg.ID.String() != "example.com/sandbox/shapes" {
		t.Errorf("Prod = %q %q, synthesized variants not filtered?", pkg.Name, pkg.ID)
	}
	// Widest-variant preference: the in-package test file folds into Prod.
	if _, ok := pkg.File(workspace.FilePath("example.com/sandbox/shapes/internal_test.go")); !ok {
		t.Error("internal_test.go not in Prod: widest variant was not preferred")
	}
	if _, ok := pkg.Symbol("TestAreaInternal"); !ok {
		t.Error("in-package test symbol not indexed")
	}

	// External test package lands in XTest with its own namespace, under
	// its production sibling's address.
	xtest := unit.XTest()
	if xtest == nil || xtest.Name != "shapes_test" {
		t.Fatalf("XTest missing or misnamed: %+v", xtest)
	}
	if xtest.ID.String() != "example.com/sandbox/shapes_test" {
		t.Errorf("XTest.ID = %q", xtest.ID)
	}
	if _, ok := xtest.Symbol("TestAreaExternal"); !ok {
		t.Error("external test symbol not indexed")
	}

	// Generic receivers unwrap to the base type name.
	if sym, ok := pkg.Symbol("Stack.Push"); !ok || sym.Recv != "Stack" {
		t.Errorf(`Symbol("Stack.Push") = %+v, generic receiver not unwrapped`, sym)
	}
	// init functions are keyless, collected per file.
	groups, ok := pkg.File(workspace.FilePath("example.com/sandbox/shapes/groups.go"))
	if !ok || len(groups.Inits) != 1 {
		t.Errorf("groups.go Inits = %v, want exactly one", groups)
	}
	// Blank identifiers are not addressable.
	if _, ok := pkg.Symbol("_"); ok {
		t.Error("blank identifier was indexed")
	}

	ws := e.ws
	for _, addr := range ws.UnitKeys() {
		u, _ := ws.Unit(addr)
		for _, p := range []*workspace.Package{u.Prod(), u.XTest()} {
			if p == nil {
				continue
			}
			for _, f := range p.Files() {
				if len(f.Src()) == 0 {
					t.Errorf("%s: empty Src, canonical-bytes invariant broken", f.Path)
				}
				if f.IsDirty() {
					t.Errorf("%s: dirty right after bootstrap", f.Path)
				}
			}
		}
	}
}

func TestBootstrapIsIdempotent(t *testing.T) {
	e := sandboxStore(t)
	first := len(e.ws.UnitKeys())
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	if len(e.ws.UnitKeys()) != first {
		t.Errorf("package count changed across re-bootstrap: %d -> %d", first, len(e.ws.UnitKeys()))
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

	e := NewStore(dir, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap must not fail on diagnostics: %v", err)
	}
	unit, ok := e.ws.Unit("example.com/broken")
	prod := unit.Prod()
	if !ok || prod == nil {
		t.Fatal("broken package missing from state")
	}
	var diags []workspace.Diagnostic
	if f, ok := prod.File("example.com/broken/main.go"); ok {
		diags = append(diags, f.Diags...)
		if !bytes.Contains(f.Src(), []byte("func main()")) {
			t.Error("broken file Src not captured")
		}
	}
	diags = append(diags, prod.Diags...)
	if len(diags) == 0 {
		t.Error("expected diagnostics for a file with a parse error, got none")
	}
}

func TestExternalLoading(t *testing.T) {
	e := sandboxStore(t)
	if err := e.LoadExternal(context.Background(), "io"); err != nil {
		t.Fatalf("LoadExternal(io): %v", err)
	}
	err := e.Read(context.Background(), func(v *View) error {
		if !v.HasExternalPackage("io") {
			t.Fatal("io missing from the external cache")
		}
		if _, ok := v.Symbol("io", "Reader"); !ok {
			t.Fatal("io.Reader not indexed")
		}
		src, ok := v.DeclSource("io", "Reader")
		if !ok || !strings.Contains(src, "Read(p []byte) (n int, err error)") {
			t.Errorf("DeclSource(io.Reader) = %q, %v", src, ok)
		}
		syms, ok := v.PackageSymbols(pkgID("io", "io"))
		if !ok {
			t.Fatal("io package symbols not found")
		}
		for _, sym := range syms {
			if r := sym.Key[0]; r >= 'a' && r <= 'z' {
				t.Errorf("unexported symbol %q leaked into the external index", sym.Key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExternalRefusalsAndReset(t *testing.T) {
	e := sandboxStore(t)
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
		return tx.CreateSymbol(pkgID("io", "io"), "extra.go", "func Nope() {}")
	}); err == nil || !strings.Contains(err.Error(), "no package") {
		t.Errorf("mutating a dependency must fail, got %v", err)
	}
	// The cache lives and dies with the workspace snapshot.
	if _, err := e.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	e.Read(context.Background(), func(v *View) error {
		if v.HasExternalPackage("io") {
			t.Error("external cache survived reload")
		}
		return nil
	})
}

// TestLoadExternalConcurrent exercises the double-checked-locking path in
// LoadExternal: many goroutines racing to load the same not-yet-cached
// dependency must all succeed, with the package installed exactly once.
// Run with -race; this is the regression test for the lock-narrowing that
// lets the slow packages.Load/type-check phase run without holding
// Store.mu.
func TestLoadExternalConcurrent(t *testing.T) {
	e := sandboxStore(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = e.LoadExternal(context.Background(), "io")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: LoadExternal(io): %v", i, err)
		}
	}
	err := e.Read(context.Background(), func(v *View) error {
		if !v.HasExternalPackage("io") {
			t.Fatal("io missing from the external cache after concurrent loads")
		}
		if _, ok := v.Symbol("io", "Reader"); !ok {
			t.Error("io.Reader not indexed after concurrent loads")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReadEditStress exercises the RWMutex directly, beyond
// TestLoadExternalConcurrent's narrower double-checked-locking case:
// several Reads and several Edits (independent packages, plus one
// importer/importee pair, plus the heavily-read shapes package) run
// concurrently under -race. Reads must never observe a torn or
// half-applied Edit; nothing here should race or error.
func TestConcurrentReadEditStress(t *testing.T) {
	e := sandboxStore(t)
	ctx := context.Background()

	const readIterations = 60
	const editIterations = 8
	var wg sync.WaitGroup
	errs := make(chan error, 256)

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < readIterations; j++ {
				err := e.Read(ctx, func(v *View) error {
					if _, ok := v.Symbol(spkg("shapes"), "NotShape"); !ok {
						return errors.New("NotShape missing during concurrent read")
					}
					_ = v.AllDiagnostics()
					return nil
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	type editTarget struct {
		pkg    workspace.PackagePath
		key    string
		bodies [2]string
	}
	edits := []editTarget{
		{"example.com/sandbox/mvalpha", "Solo", [2]string{"func Solo() int { return 1 }", "func Solo() int { return 2 }"}},
		{"example.com/sandbox/mvbeta", "AlreadyHere", [2]string{"func AlreadyHere() int { return mvalpha.Solo() }", "func AlreadyHere() int { return mvalpha.Solo() + 1 }"}},
		{"example.com/sandbox/mvdest", "Existing", [2]string{"func Existing() int { return 0 }", "func Existing() int { return 1 }"}},
		{spkg("shapes"), "NotShape", [2]string{"type NotShape struct{}", "type NotShape struct{ pad int }"}},
	}
	for _, ed := range edits {
		wg.Add(1)
		go func(ed editTarget) {
			defer wg.Done()
			for j := 0; j < editIterations; j++ {
				if _, err := e.Edit(ctx, func(tx *Tx) error {
					return tx.EditSymbol(ed.pkg, ed.key, ed.bodies[j%2])
				}); err != nil {
					errs <- err
					return
				}
			}
		}(ed)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	assertModelEqualsDisk(t, e)
}
