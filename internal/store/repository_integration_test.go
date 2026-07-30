package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

func TestFlushWritesAndUnlinks(t *testing.T) {
	root := copySandbox(t)
	e := NewStore(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	mustEdit(t, e, func(tx *Tx) error {
		if err := tx.CreateSymbol(spkgID("shapes"), "extra.go", "func Twice(x float64) float64 { return 2 * x }"); err != nil {
			return err
		}
		return tx.DeleteFile(spkg("broken"), "broken.go")
	})

	written, removed, err := e.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if !slices.Contains(written, sfile("shapes", "extra.go")) {
		t.Errorf("Flush written = %v, missing extra.go", written)
	}
	if !slices.Contains(removed, sfile("broken", "broken.go")) {
		t.Errorf("Flush removed = %v, missing broken.go", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes", "extra.go")); err != nil {
		t.Errorf("extra.go not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "broken", "broken.go")); !os.IsNotExist(err) {
		t.Errorf("broken.go still on disk: %v", err)
	}
}

func TestFlushKeepsNonEmptySourceDir(t *testing.T) {
	root := copySandbox(t)
	e := NewStore(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveFile(spkg("mvsrc"), "standalone.go", spkg("mvdest"), "")
	})
	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "mvsrc")); err != nil {
		t.Errorf("source directory with remaining files was removed: %v", err)
	}
}

func TestFlushRemovesEmptyDirAfterPackageMove(t *testing.T) {
	root := copySandbox(t)
	e := NewStore(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	mustEdit(t, e, func(tx *Tx) error {
		return tx.MovePackage(spkg("shapes"), spkg("geo"))
	})
	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes")); !os.IsNotExist(err) {
		t.Errorf("old package directory still on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "geo")); err != nil {
		t.Errorf("new package directory missing: %v", err)
	}
}

// TestModelMatchesDiskAfterFlush is the equivalence oracle's own
// regression test: after a mixed batch of edits and a Flush, a fresh
// Bootstrap of the same root must produce an identical model. Exercises
// create, move, and delete together so the oracle itself is proven
// against more than one verb before steps 4 and 5 start relying on it.
func TestModelMatchesDiskAfterFlush(t *testing.T) {
	root := copySandbox(t)
	e := NewStore(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	mustEdit(t, e, func(tx *Tx) error {
		if err := tx.CreateSymbol(spkgID("shapes"), "extra.go", "func Twice(x float64) float64 { return 2 * x }"); err != nil {
			return err
		}
		if err := tx.MoveFile(spkg("shapes"), "groups.go", "", "extras.go"); err != nil {
			return err
		}
		return tx.DeleteFile(spkg("broken"), "broken.go")
	})
	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	assertModelEqualsDisk(t, e)
}

// TestModelMatchesDiskAfterGroupAndMethodMutations stresses the oracle
// against structural shapes the other regression test doesn't touch: a
// position-dependent (iota) const group relocated whole to a new file,
// then edited to add a member, and a method relocated to a new file.
// Each has splice/propagation machinery distinct from a plain function or
// file move.
func TestModelMatchesDiskAfterGroupAndMethodMutations(t *testing.T) {
	root := copySandbox(t)
	e := NewStore(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	mustEdit(t, e, func(tx *Tx) error {
		if err := tx.MoveSymbol(spkg("shapes"), "KindSquare", "", "kinds.go", ""); err != nil {
			return err
		}
		if err := tx.EditSymbol(spkg("shapes"), "KindSquare",
			"// KindCircle is the round one.\nKindCircle Kind = iota\nKindSquare\nKindTriangle"); err != nil {
			return err
		}
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "shapes_extra.go", "")
	})
	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	assertModelEqualsDisk(t, e)
}

func TestReloadDiscards(t *testing.T) {
	e := sandboxStore(t)
	mustEdit(t, e, func(tx *Tx) error {
		if err := tx.CreateSymbol(spkgID("shapes"), "extra.go", "func Extra() {}"); err != nil {
			return err
		}
		return tx.DeleteFile(spkg("use"), "alias.go")
	})
	discarded, err := e.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	for _, want := range []workspace.FilePath{sfile("shapes", "extra.go"), sfile("use", "alias.go")} {
		if !slices.Contains(discarded, want) {
			t.Errorf("discarded missing %q: %v", want, discarded)
		}
	}
	if _, ok := resolveSymbol(e, spkg("shapes"), "Extra"); ok {
		t.Error("unflushed symbol survived reload")
	}
	if _, _, ok := resolveFile(e, sfile("use", "alias.go")); !ok {
		t.Error("unflushed deletion survived reload: alias.go missing")
	}
}

func TestMoveFileAndFlush(t *testing.T) {
	root := copySandbox(t)
	e := NewStore(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveFile(spkg("shapes"), "groups.go", "", "extras.go")
	})
	if len(report.Delta) != 0 {
		t.Errorf("file move introduced diagnostics: %v", deltaStrings(report))
	}
	for _, want := range []workspace.FilePath{sfile("shapes", "groups.go"), sfile("shapes", "extras.go")} {
		if !slices.Contains(report.Changed, want) {
			t.Errorf("Changed = %v, missing %s", report.Changed, want)
		}
	}
	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes", "extras.go")); err != nil {
		t.Errorf("moved file not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes", "groups.go")); !os.IsNotExist(err) {
		t.Errorf("old path still on disk: %v", err)
	}
}

func TestCreatePackageThroughRecheck(t *testing.T) {
	e := sandboxStore(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		if err := tx.CreatePackage(spkg("util"), ""); err != nil {
			return err
		}
		return tx.CreateSymbol(spkgID("util"), "util.go", "func Half(x float64) float64 { return x / 2 }")
	})
	if len(report.Delta) != 0 {
		t.Errorf("new package produced diagnostics: %v", deltaStrings(report))
	}
	pkg, ok := resolvePackage(e, spkg("util"))
	if !ok {
		t.Fatal("util package missing after recheck — overlay-only directories not surviving the reload")
	}
	if pkg.ID.String() != "example.com/sandbox/util" {
		t.Errorf("recheck did not resolve the import path: %q", pkg.ID)
	}
	if _, ok := resolveSymbol(e, spkg("util"), "Half"); !ok {
		t.Error("Half not resolvable in the new package")
	}
}
