package workspace

import (
	"go/token"
	"testing"
)

func TestUnitDirtyCarryAndPrune(t *testing.T) {
	w := NewWorkspace()
	w.Reset("example.com/mod", token.NewFileSet(), map[PackagePath]*Unit{})
	w.InstallUnit("example.com/mod/pkg", NewUnit(&Package{Name: "pkg", ID: newPackageID("example.com/mod/pkg", KindProd)}, nil))
	if err := w.SwapFile("example.com/mod/pkg", false, "example.com/mod/pkg/pkg.go", []byte("package pkg\n\nfunc Hello() {}\n")); err != nil {
		t.Fatal(err)
	}
	w.MarkFlushed("example.com/mod/pkg", false, "example.com/mod/pkg/pkg.go")
	unit, _ := w.Unit("example.com/mod/pkg")
	p := unit.Prod()
	u := NewUnit(p, nil)
	u.MarkDirty("example.com/mod/pkg/pkg.go")
	file, _ := p.File("example.com/mod/pkg/pkg.go")
	if !file.IsDirty() {
		t.Error("MarkDirty did not re-mark the carried-over file")
	}
	units := map[PackagePath]*Unit{"example.com/mod/pkg": u}
	DropTombstonedFile(units, "example.com/mod/pkg", "example.com/mod/pkg/pkg.go")
	if _, ok := units["example.com/mod/pkg"]; ok {
		t.Error("unit must be pruned once its last file is gone")
	}
}
