package workspace

import (
	"go/token"
	"testing"
)

func TestMemberDirtyCarryAndPrune(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[PackagePath]*Package{}, map[PackagePath]*Package{})
	w.InstallProd("example.com/mod/pkg", &Package{Name: "pkg", ID: newPackageID("example.com/mod/pkg", KindProd)})
	if err := w.SwapFile("example.com/mod/pkg", KindProd, false, "example.com/mod/pkg/pkg.go", []byte("package pkg\n\nfunc Hello() {}\n")); err != nil {
		t.Fatal(err)
	}
	w.MarkFlushed("example.com/mod/pkg", KindProd, "example.com/mod/pkg/pkg.go")
	p, _ := w.ProdPackage("example.com/mod/pkg")
	prod := map[PackagePath]*Package{"example.com/mod/pkg": p}
	xtest := map[PackagePath]*Package{}
	MarkFileDirty(prod, xtest, "example.com/mod/pkg", "example.com/mod/pkg/pkg.go")
	file, _ := p.File("example.com/mod/pkg/pkg.go")
	if !file.IsDirty() {
		t.Error("MarkFileDirty did not re-mark the carried-over file")
	}
	DropTombstonedFile(prod, xtest, "example.com/mod/pkg", "example.com/mod/pkg/pkg.go")
	if _, ok := prod["example.com/mod/pkg"]; ok {
		t.Error("package must be pruned once its last file is gone")
	}
}
