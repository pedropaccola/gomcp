package tools

import (
	"context"
	"strings"
	"testing"
)

func TestAddressForms(t *testing.T) {
	eng := sandboxEngine(t)

	// Package arguments never accept file names, on any tool.
	if _, _, err := listSymbols(eng, testCfg())(context.Background(), nil, ListSymbolsInput{
		PkgPath: "shapes/shapes.go",
	}); err == nil || !strings.Contains(err.Error(), "names a file") {
		t.Errorf("file-named package must be refused, got %v", err)
	}
	if _, _, err := deletePackage(eng, testCfg())(context.Background(), nil, DeletePackageInput{
		Deletes: []DeletePackageEntry{{PkgPath: "shapes/shapes.go"}},
	}); err == nil || !strings.Contains(err.Error(), "names a file") {
		t.Errorf("file-named package on a destructive tool must be refused, got %v", err)
	}

	// File arguments accept a bare name or a path that agrees with the
	// package; contradictions and non-*.go forms are refused.
	if _, syms, err := listSymbols(eng, testCfg())(context.Background(), nil, ListSymbolsInput{
		PkgPath: "shapes", FileName: new("shapes/groups.go"),
	}); err != nil || len(syms.Symbols) == 0 {
		t.Errorf("file path agreeing with package must be accepted, got %v", err)
	}
	if _, _, err := listSymbols(eng, testCfg())(context.Background(), nil, ListSymbolsInput{
		PkgPath: "shapes", FileName: new("use/use.go"),
	}); err == nil || !strings.Contains(err.Error(), "does not live in") {
		t.Errorf("file outside the package must be refused, got %v", err)
	}
	if _, _, err := createSymbol(eng, testCfg())(context.Background(), nil, CreateSymbolInput{
		Creates: []CreateSymbolEntry{{PkgPath: "shapes", FileName: "notgo", Source: "func X() {}"}},
	}); err == nil || !strings.Contains(err.Error(), "bare *.go name") {
		t.Errorf("non-.go file name must be refused, got %v", err)
	}

	// File-addressed mutations speak (package, file) like everything else.
	if _, _, err := deleteFile(eng, testCfg())(context.Background(), nil, DeleteFileInput{
		Deletes: []DeleteFileEntry{{PkgPath: "use", FileName: "alias.go"}},
	}); err != nil {
		t.Errorf("delete_file with (package, file): %v", err)
	}
	if _, _, err := moveFile(eng, testCfg())(context.Background(), nil, MoveFileInput{
		PkgPath: "shapes", FileName: "shapes/groups.go", NewFileName: new("grouped.go"),
	}); err != nil {
		t.Errorf("move_file with an agreeing file path: %v", err)
	}
}
