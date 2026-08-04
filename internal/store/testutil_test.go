package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

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

// sandboxStore bootstraps the fixture module. Mutations stay in memory;
// tests that Flush must use copySandbox instead.
func sandboxStore(tb testing.TB) *Store {
	tb.Helper()
	e := NewStore(filepath.Join(moduleRoot(tb), "testdata", "sandbox"), nil)
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

func matchKeys(matches []Symbol) []string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Owner.String()+":"+m.Key)
	}
	return out
}

// spkg addresses a sandbox package the way the store now expects:
// module-qualified.
func spkg(dir string) workspace.PackagePath {
	return workspace.PackagePath("example.com/sandbox/" + dir)
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
func assertModelEqualsDisk(tb testing.TB, e *Store) {
	tb.Helper()
	fresh := NewStore(e.RootDir, nil)
	if err := fresh.Bootstrap(context.Background()); err != nil {
		tb.Fatalf("assertModelEqualsDisk: fresh Bootstrap: %v", err)
	}

	got, want := e.ws, fresh.ws
	gotView, wantView := NewView(got, context.Background()), NewView(want, context.Background())

	gotKeys, wantKeys := got.MemberKeys(), want.MemberKeys()
	if !slices.Equal(gotKeys, wantKeys) {
		tb.Fatalf("unit set diverged: got %v, want %v", gotKeys, wantKeys)
	}
	xtestOf := func(ws *workspace.Workspace, addr workspace.PackagePath) *workspace.Package {
		for _, p := range ws.MembersOf(addr) {
			if p.ID.Kind() == workspace.KindXTest {
				return p
			}
		}
		return nil
	}
	for _, addr := range wantKeys {
		gotProd, _ := got.ProdPackage(addr)
		wantProd, _ := want.ProdPackage(addr)
		diffPackagePair(tb, gotView, wantView, addr, "Prod", gotProd, wantProd)
		diffPackagePair(tb, gotView, wantView, addr, "XTest", xtestOf(got, addr), xtestOf(want, addr))
	}

	if gotDiags, wantDiags := gotView.AllDiagnostics(), wantView.AllDiagnostics(); !slices.Equal(gotDiags, wantDiags) {
		tb.Errorf("diagnostics diverged:\ngot:  %v\nwant: %v", gotDiags, wantDiags)
	}
}

// diffPackagePair compares one Prod or XTest package belonging to the same
// unit address; either side may be nil.
func diffPackagePair(tb testing.TB, gotView, wantView *View, addr workspace.PackagePath, half string, got, want *workspace.Package) {
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
		_ = i
		gotSrc, _ := gotView.DeclSource(addr, wantSym.Key(), "")
		wantSrc, _ := wantView.DeclSource(addr, wantSym.Key(), "")
		if gotSrc != wantSrc {
			tb.Errorf("%s.%s: declaration source diverged:\ngot:\n%s\nwant:\n%s", addr, wantSym.Key(), gotSrc, wantSrc)
		}
	}
}

func filePaths(files []*workspace.File) []workspace.FilePath {
	out := make([]workspace.FilePath, len(files))
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

// resolveFile gives tests raw access to a file's underlying workspace
// pointers, which View's own public API doesn't expose (it returns
// dto.File, not *workspace.File).
func resolveFile(e *Store, path workspace.FilePath) (*workspace.File, *workspace.Package, bool) {
	pkgPath := workspace.PackagePath(filepath.Dir(string(path)))
	for _, pkg := range e.ws.MembersOf(pkgPath) {
		if file, ok := pkg.File(path); ok {
			return file, pkg, true
		}
	}
	return nil, nil, false
}

// resolvePackage gives tests raw access to a package's underlying
// workspace pointer, which View's own public API doesn't expose.
func resolvePackage(e *Store, pkg workspace.PackagePath) (*workspace.Package, bool) {
	return e.ws.ProdPackage(pkg)
}

// resolveXTest gives tests raw access to an XTest package's underlying
// workspace pointer, which View's own public API doesn't expose.
func resolveXTest(e *Store, pkg workspace.PackagePath) (*workspace.Package, bool) {
	for _, p := range e.ws.MembersOf(pkg) {
		if p.ID.Kind() == workspace.KindXTest {
			return p, true
		}
	}
	return nil, false
}

// resolveSymbol looks up pkg's key through View's own public Symbol
// method — the same read path production code uses, not a private
// reimplementation of that resolution order kept in sync by hand.
func resolveSymbol(e *Store, pkg workspace.PackagePath, key string) (Symbol, bool) {
	var sym Symbol
	var found bool
	_ = e.Read(context.Background(), func(v *View) error {
		sym, found = v.Symbol(pkg, key)
		return nil
	})
	return sym, found
}

func deltaStrings(report *EditReport) []string {
	out := make([]string, 0, len(report.Delta))
	for _, d := range report.Delta {
		out = append(out, d.String())
	}
	return out
}

func mustEdit(t *testing.T, e *Store, fn func(*Tx) error) *EditReport {
	t.Helper()
	report, err := e.Edit(context.Background(), fn)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if report.Stale {
		t.Fatalf("recheck unavailable: %s", report.Note)
	}
	return report
}

// sfile addresses a sandbox file the way the store now expects:
// module-qualified, dir being the package's own bare directory name.
func sfile(dir, name string) workspace.FilePath {
	return workspace.FilePath("example.com/sandbox/" + dir + "/" + name)
}

// spkgPath addresses a sandbox package as a canonical address.
func spkgPath(dir string) workspace.PackagePath {
	id, err := workspace.NewPackagePath("example.com/sandbox", dir)
	if err != nil {
		panic(err)
	}
	return id
}

// tpkgPath addresses a package in the "test.mod"-module unit fixtures
// (viewFixture/testutil.SimpleFixture) as a canonical address.
func tpkgPath(dir string) workspace.PackagePath {
	id, err := workspace.NewPackagePath("test.mod", dir)
	if err != nil {
		panic(err)
	}
	return id
}

// pkgPath builds a canonical address for an ad-hoc module+address pair —
// the general form spkgPath/tpkgPath specialize.
func pkgPath(module, addr string) workspace.PackagePath {
	id, err := workspace.NewPackagePath(workspace.PackagePath(module), addr)
	if err != nil {
		panic(err)
	}
	return id
}
