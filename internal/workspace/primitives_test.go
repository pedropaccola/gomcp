package workspace

import (
	"fmt"
	"go/token"
	"testing"
)

func TestSwapFileParseEnforcedAndDirty(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[PackagePath]*Package{}, map[PackagePath]*Package{})
	w.InstallProd("example.com/mod/pkg", &Package{Name: "pkg", ID: newPackageID("example.com/mod/pkg", KindProd)})
	if err := w.SwapFile("example.com/mod/pkg", KindProd, false, "example.com/mod/pkg/pkg.go", []byte("package pkg\n\nfunc broken( {}\n")); err == nil {
		t.Fatal("SwapFile accepted unparseable bytes")
	}
	p, _ := w.ProdPackage("example.com/mod/pkg")
	if len(p.Files()) != 0 {
		t.Fatal("failed swap must leave the package untouched")
	}
	if err := w.SwapFile("example.com/mod/pkg", KindProd, false, "example.com/mod/pkg/pkg.go", []byte("package pkg\n\nfunc Hello() {}\n")); err != nil {
		t.Fatalf("SwapFile: %v", err)
	}
	p, _ = w.ProdPackage("example.com/mod/pkg")
	file, ok := p.File("example.com/mod/pkg/pkg.go")
	if !ok || !file.IsDirty() {
		t.Fatal("swapped file missing or not dirty")
	}
	if _, ok := p.Symbol("Hello"); !ok {
		t.Error("index not rebuilt by SwapFile")
	}
	w.MarkFlushed("example.com/mod/pkg", KindProd, "example.com/mod/pkg/pkg.go")
	p, _ = w.ProdPackage("example.com/mod/pkg")
	file, ok = p.File("example.com/mod/pkg/pkg.go")
	if !ok || file.IsDirty() {
		t.Error("MarkFlushed did not clear the dirty mark")
	}
}

func TestCloneSharesUntouchedPackages(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	clone := w.Clone()
	p, _ := w.ProdPackage("test.mod/pkg")
	clonedP, _ := clone.ProdPackage("test.mod/pkg")
	if p != clonedP {
		t.Error("Clone must share an untouched package's pointer, not copy it eagerly")
	}
}

func TestCloneIsolatesMutationsFromOriginal(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	clone := w.Clone()
	if err := clone.SwapFile("test.mod/pkg", KindProd, false, "test.mod/pkg/pkg.go", []byte("package pkg\n\nfunc Bar() {}\n")); err != nil {
		t.Fatalf("SwapFile on clone: %v", err)
	}
	p, _ := w.ProdPackage("test.mod/pkg")
	if _, ok := p.Symbol("Foo"); !ok {
		t.Error("mutating the clone must not affect the original's Foo")
	}
	if _, ok := p.Symbol("Bar"); ok {
		t.Error("the original must not see the clone's Bar")
	}
	clonedP, _ := clone.ProdPackage("test.mod/pkg")
	if _, ok := clonedP.Symbol("Bar"); !ok {
		t.Error("the clone itself must see its own Bar")
	}
}

func TestForkExternalIndependentCache(t *testing.T) {
	w := NewWorkspace()
	w.Reset("test.mod", token.NewFileSet(), map[PackagePath]*Package{}, map[PackagePath]*Package{})
	forked := w.ForkExternal()
	forked.FailExternal("dep", fmt.Errorf("boom"))
	if _, ok := w.ExternalFailure("dep"); ok {
		t.Error("ForkExternal must not let the fork's cache writes leak back to the original")
	}
	if _, ok := forked.ExternalFailure("dep"); !ok {
		t.Error("the fork itself must see its own cache write")
	}
}

func TestDropFilePrunesEmptyMembers(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	w.DropFile("test.mod/pkg", KindProd, "test.mod/pkg/pkg.go")
	if _, ok := w.ProdPackage("test.mod/pkg"); ok {
		t.Error("a unit whose last file was dropped must be pruned")
	}
	if _, ok := w.TombstoneMask("test.mod/pkg/pkg.go"); !ok {
		t.Error("DropFile must tombstone the removed path")
	}
}

func TestMoveFileTombstonesOldPathAndMarksDirty(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	w.MoveFile("test.mod/pkg", KindProd, "test.mod/pkg/pkg.go", "test.mod/pkg/renamed.go")
	if _, ok := w.TombstoneMask("test.mod/pkg/pkg.go"); !ok {
		t.Error("MoveFile must tombstone the old path")
	}
	if _, ok := w.TombstoneMask("test.mod/pkg/renamed.go"); ok {
		t.Error("MoveFile must not leave the new path tombstoned")
	}
	p, _ := w.ProdPackage("test.mod/pkg")
	file, ok := p.File("test.mod/pkg/renamed.go")
	if !ok || !file.IsDirty() {
		t.Error("the moved file must exist at its new path and be marked dirty")
	}
	if _, ok := p.File("test.mod/pkg/pkg.go"); ok {
		t.Error("the old path must no longer resolve")
	}
}
