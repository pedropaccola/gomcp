package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecheckScopeCarriesForwardUnaffectedPackages proves Recheck v2's
// narrowing actually narrows: editing package a (imported by b, unrelated
// to c) must recheck a and sweep in its importer b, while c's
// *workspace.Package survives untouched — same pointer, not rebuilt.
func TestRecheckScopeCarriesForwardUnaffectedPackages(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/recheck\n\ngo 1.21\n")
	write("a/a.go", "package a\n\nfunc X() int { return 1 }\n")
	write("b/b.go", "package b\n\nimport \"example.com/recheck/a\"\n\nfunc Y() int { return a.X() }\n")
	write("c/c.go", "package c\n\nfunc Z() int { return 2 }\n")

	e := NewStore(dir, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	cUnit, ok := e.ws.Unit("example.com/recheck/c")
	if !ok || cUnit.Prod() == nil {
		t.Fatal("c unit missing after bootstrap")
	}
	cBefore := cUnit.Prod()

	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.EditSymbol("example.com/recheck/a", "X", "func X() int { return 2 }")
	}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	cAfter, ok := e.ws.Unit("example.com/recheck/c")
	if !ok || cAfter.Prod() != cBefore {
		t.Error("c's Package was rebuilt: Recheck v2 did not narrow the recheck")
	}
	if !e.ws.NarrowlyChecked() {
		t.Error("generation should be marked narrowlyChecked after a scoped recheck")
	}

	bUnit, ok := e.ws.Unit("example.com/recheck/b")
	if !ok || bUnit.Prod() == nil {
		t.Fatal("b unit missing after edit")
	}
	if _, ok := bUnit.Prod().Symbol("Y"); !ok {
		t.Error("b's own symbol missing after being swept into the recheck scope")
	}

	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	assertModelEqualsDisk(t, e)
}

// TestEnsureFullyCheckedClearsNarrowFlag isolates EnsureFullyChecked from
// the tools-layer retry path: after a narrow recheck, NarrowlyChecked
// must be true, and false again once EnsureFullyChecked returns.
func TestEnsureFullyCheckedClearsNarrowFlag(t *testing.T) {
	eng := sandboxStore(t)

	if _, err := eng.Edit(context.Background(), func(tx *Tx) error {
		return tx.EditSymbol("example.com/sandbox/mvdest", "Existing", "func Existing() int { return 1 }")
	}); err != nil {
		t.Fatalf("Edit(mvdest): %v", err)
	}
	if !eng.ws.NarrowlyChecked() {
		t.Fatal("expected NarrowlyChecked after editing a leaf package")
	}

	if err := eng.EnsureFullyChecked(context.Background()); err != nil {
		t.Fatalf("EnsureFullyChecked: %v", err)
	}
	if eng.ws.NarrowlyChecked() {
		t.Error("NarrowlyChecked should be false after EnsureFullyChecked")
	}
}

// TestAbortedEditThenSuccessfulEditIsClean proves an aborted transaction
// that already spliced at least one file (SwapFile ran, adding entries to
// the then-current, shared FileSet) leaves no trace once a later,
// successful edit publishes a fresh generation: error means nothing
// happened for the aborted attempt, and the next successful edit's own
// freshly assembled FileSet has no dependency on whatever the aborted one
// left behind.
func TestAbortedEditThenSuccessfulEditIsClean(t *testing.T) {
	e := sandboxStore(t)

	sentinel := errors.New("forced abort after a real splice")
	_, err := e.Edit(context.Background(), func(tx *Tx) error {
		if err := tx.EditSymbol(spkg("shapes"), "NotShape", "type NotShape struct{ aborted bool }"); err != nil {
			t.Fatalf("EditSymbol before abort: %v", err)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Edit error = %v, want sentinel", err)
	}

	// error means nothing happened: the published workspace must still
	// show the pre-abort source, not the splice the aborted fn(tx) applied.
	src, ok := e.ws.DeclSource(spkg("shapes"), "NotShape")
	if !ok {
		t.Fatal("NotShape missing after aborted edit")
	}
	if strings.Contains(src, "aborted") {
		t.Errorf("NotShape = %q, aborted edit's splice leaked into the published workspace", src)
	}

	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.EditSymbol(spkg("shapes"), "NotShape", "type NotShape struct{ recovered bool }")
	}); err != nil {
		t.Fatalf("Edit after abort: %v", err)
	}
	src, ok = e.ws.DeclSource(spkg("shapes"), "NotShape")
	if !ok || !strings.Contains(src, "recovered") {
		t.Errorf("NotShape = %q, want the post-abort edit to apply cleanly", src)
	}

	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	assertModelEqualsDisk(t, e)
}
