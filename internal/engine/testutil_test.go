package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// moduleRoot walks up from the package directory to the go.mod.
func moduleRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}

// sandboxEngine bootstraps the fixture module. Mutations stay in memory;
// tests that Flush must use copySandbox instead.
func sandboxEngine(tb testing.TB) *Engine {
	tb.Helper()
	e := NewEngine(filepath.Join(moduleRoot(tb), "testdata", "sandbox"), nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		tb.Fatalf("Bootstrap: %v", err)
	}
	return e
}

// copySandbox clones the fixture module into a scratch dir so Flush tests
// can touch disk without contaminating testdata.
func copySandbox(tb testing.TB) string {
	tb.Helper()
	src := filepath.Join(moduleRoot(tb), "testdata", "sandbox")
	dst := tb.TempDir()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		tb.Fatal(err)
	}
	return dst
}

func matchKeys(matches []Match) []string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Pkg.Path().String()+":"+m.Sym.Key())
	}
	return out
}

// spkg addresses a sandbox package the way the engine now expects:
// module-qualified.
func spkg(dir string) address.PkgPath {
	return address.PkgPath("example.com/sandbox/" + dir)
}

// assertModelEqualsDisk verifies e's in-memory model matches what a fresh
// Bootstrap of e's own RootDir produces: same units (Prod and XTest
// alike), same files (path and doc comment), same symbols (set and
// declaration source), same workspace-level diagnostics. e must already
// be flushed — this only catches divergence between a tracked model and
// disk, not unflushed edits. This is the equivalence oracle steps 4 (lazy
// copy-on-write) and 5 (incremental recheck) gate on: both introduce a
// way for the model to silently drift from a full rebuild without an
// error surfacing anywhere else. Reports every divergence found rather
// than stopping at the first, so a regression shows its full blast
// radius in one run.
func assertModelEqualsDisk(tb testing.TB, e *Engine) {
	tb.Helper()
	fresh := NewEngine(e.RootDir, nil)
	if err := fresh.Bootstrap(context.Background()); err != nil {
		tb.Fatalf("assertModelEqualsDisk: fresh Bootstrap: %v", err)
	}

	got, want := e.ws.Load(), fresh.ws.Load()
	gotView, wantView := &View{eng: e, ws: got}, &View{eng: fresh, ws: want}

	gotKeys, wantKeys := got.UnitKeys(), want.UnitKeys()
	if !slices.Equal(gotKeys, wantKeys) {
		tb.Fatalf("unit set diverged: got %v, want %v", gotKeys, wantKeys)
	}
	for _, addr := range wantKeys {
		gotUnit, _ := got.Unit(addr)
		wantUnit, _ := want.Unit(addr)
		diffPackagePair(tb, gotView, wantView, addr, "Prod", gotUnit.Prod, wantUnit.Prod)
		diffPackagePair(tb, gotView, wantView, addr, "XTest", gotUnit.XTest, wantUnit.XTest)
	}

	if gotDiags, wantDiags := gotView.AllDiagnostics(), wantView.AllDiagnostics(); !slices.Equal(gotDiags, wantDiags) {
		tb.Errorf("diagnostics diverged:\ngot:  %v\nwant: %v", gotDiags, wantDiags)
	}
}

// diffPackagePair compares one Prod or XTest package belonging to the same
// unit address; either side may be nil.
func diffPackagePair(tb testing.TB, gotView, wantView *View, addr address.PkgPath, half string, got, want *workspace.Package) {
	tb.Helper()
	if (got == nil) != (want == nil) {
		tb.Errorf("%s (%s): presence diverged: got %v, want %v", addr, half, got != nil, want != nil)
		return
	}
	if got == nil {
		return
	}
	if got.Doc() != want.Doc() {
		tb.Errorf("%s (%s): package doc diverged: got %q, want %q", addr, half, got.Doc(), want.Doc())
	}

	gotFiles, wantFiles := got.Files(), want.Files()
	gotPaths, wantPaths := filePaths(gotFiles), filePaths(wantFiles)
	if !slices.Equal(gotPaths, wantPaths) {
		tb.Errorf("%s (%s): file set diverged: got %v, want %v", addr, half, gotPaths, wantPaths)
	} else {
		for i, wf := range wantFiles {
			if gf := gotFiles[i]; gf.Doc() != wf.Doc() {
				tb.Errorf("%s: file doc diverged: got %q, want %q", gf.Path, gf.Doc(), wf.Doc())
			}
		}
	}

	gotSyms, wantSyms := got.Symbols(), want.Symbols()
	gotSymKeys, wantSymKeys := symbolKeys(gotSyms), symbolKeys(wantSyms)
	if !slices.Equal(gotSymKeys, wantSymKeys) {
		tb.Errorf("%s (%s): symbol set diverged: got %v, want %v", addr, half, gotSymKeys, wantSymKeys)
		return
	}
	for i, wantSym := range wantSyms {
		gotSrc, _ := gotView.declSource(gotSyms[i])
		wantSrc, _ := wantView.declSource(wantSym)
		if !bytes.Equal(gotSrc, wantSrc) {
			tb.Errorf("%s.%s: declaration source diverged:\ngot:\n%s\nwant:\n%s", addr, wantSym.Key(), gotSrc, wantSrc)
		}
	}
}

func filePaths(files []*workspace.File) []address.RelativePath {
	out := make([]address.RelativePath, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func symbolKeys(symbols []*workspace.Symbol) []string {
	out := make([]string, len(symbols))
	for i, s := range symbols {
		out[i] = s.Key()
	}
	return out
}
