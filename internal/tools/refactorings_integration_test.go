package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestMoveFileEchoKeepsNonVacatedSource(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := moveFile(eng, testCfg())(context.Background(), nil, MoveFileInput{
		PkgPath: "mvsrc", FileName: "standalone.go", NewPkgPath: new("mvdest"),
	})
	if err != nil {
		t.Fatalf("move_file: %v", err)
	}
	if _, ok := out.Files["example.com/sandbox/mvsrc"]; !ok {
		t.Errorf("echo dropped a still-existing source package: %+v", out.Files)
	}
	if _, ok := out.Files["example.com/sandbox/mvdest"]; !ok {
		t.Errorf("echo missing the destination package: %+v", out.Files)
	}
}

func TestMoveFileEchoKeepsSamePackageRenameTogether(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := moveFile(eng, testCfg())(context.Background(), nil, MoveFileInput{
		PkgPath: "shapes", FileName: "groups.go", NewFileName: new("groups2.go"),
	})
	if err != nil {
		t.Fatalf("move_file: %v", err)
	}
	files := out.Files["example.com/sandbox/shapes"]
	if !slices.Contains(files, "groups.go") || !slices.Contains(files, "groups2.go") {
		t.Errorf("same-package rename should list both names under one bucket, got %+v", out.Files)
	}
}

func TestMoveFileEchoOmitsVacatedSource(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := moveFile(eng, testCfg())(context.Background(), nil, MoveFileInput{
		PkgPath: "mvalpha", FileName: "mvalpha.go", NewPkgPath: new("mvbeta"),
	})
	if err != nil {
		t.Fatalf("move_file: %v", err)
	}
	if _, ok := out.Files["example.com/sandbox/mvalpha"]; ok {
		t.Errorf("echo still lists the vacated source package: %+v", out.Files)
	}
	if _, ok := out.Files["example.com/sandbox/mvbeta"]; !ok {
		t.Errorf("echo missing the destination package: %+v", out.Files)
	}
}

func TestMovePackageEchoOmitsVacatedSource(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := movePackage(eng, testCfg())(context.Background(), nil, MovePackageInput{
		PkgPath: "shapes", NewPkgPath: "geo",
	})
	if err != nil {
		t.Fatalf("move_package: %v", err)
	}
	if _, ok := out.Files["example.com/sandbox/shapes"]; ok {
		t.Errorf("echo still lists the vacated source package: %+v", out.Files)
	}
	if _, ok := out.Files["example.com/sandbox/geo"]; !ok {
		t.Errorf("echo missing the destination package: %+v", out.Files)
	}
}

func TestMoveSymbolInputWiring(t *testing.T) {
	eng := sandboxEngine(t)

	if _, _, err := moveSymbol(eng, testCfg())(context.Background(), nil, MoveSymbolInput{
		PkgPath: "shapes", SymbolKey: "NotShape", NewSymbolKey: new("AlsoNotShape"),
	}); err != nil {
		t.Fatalf("move_symbol rename: %v", err)
	}

	if _, _, err := moveSymbol(eng, testCfg())(context.Background(), nil, MoveSymbolInput{
		PkgPath: "shapes", SymbolKey: "Circle.Area", NewSymbolKey: new("Square.Extent"),
	}); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Errorf("mismatched receiver via move_symbol must be refused, got %v", err)
	}

	if _, _, err := moveSymbol(eng, testCfg())(context.Background(), nil, MoveSymbolInput{
		PkgPath: "shapes", SymbolKey: "Circle.Area", NewSymbolKey: new("Circle.Extent"),
	}); err != nil {
		t.Fatalf("move_symbol qualified method rename: %v", err)
	}
}
