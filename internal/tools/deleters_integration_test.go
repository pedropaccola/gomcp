package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestDeleteFileBatchAbortsWhollyOnFailure(t *testing.T) {
	st := sandboxStore(t)
	_, _, err := deleteFile(st, testCfg())(context.Background(), nil, DeleteFileInput{
		Deletes: []DeleteFileEntry{
			{PkgPath: "shapes", FileName: "shapes.go"},
			{PkgPath: "shapes", FileName: "notgo"},
		},
	})
	if err == nil {
		t.Fatal("batch with a malformed entry must fail")
	}
	if !strings.Contains(err.Error(), "deletes[1]") {
		t.Errorf("error must name the failing entry, got %v", err)
	}
	_, out, err := listFiles(st, testCfg())(context.Background(), nil, ListFilesInput{PkgPath: "shapes"})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if !slices.Contains(out.Files, "shapes.go") {
		t.Errorf("Error must mean untouched: shapes.go was deleted despite the batch failing, got %v", out.Files)
	}
}

func TestDeleteSymbolBatchDuplicateIsHarmless(t *testing.T) {
	// KindSquare's delete already collapses the whole iota group, taking
	// KindCircle with it; a later entry naming KindCircle must not abort
	// the batch just because the first entry already satisfied it.
	st := sandboxStore(t)
	_, out, err := deleteSymbol(st, testCfg())(context.Background(), nil, DeleteSymbolInput{
		Deletes: []DeleteSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "KindSquare"},
			{PkgPath: "shapes", SymbolKey: "KindCircle"},
		},
	})
	if err != nil {
		t.Fatalf("delete_symbol batch: %v", err)
	}
	if len(out.Files) == 0 {
		t.Errorf("batch echo missing the touched file: %+v", out)
	}
}

func TestDeleteTools(t *testing.T) {
	st := sandboxStore(t)

	_, out, err := deleteSymbol(st, testCfg())(context.Background(), nil, DeleteSymbolInput{
		Deletes: []DeleteSymbolEntry{{PkgPath: "shapes", SymbolKey: "Circle"}},
	})
	if err != nil {
		t.Fatalf("delete_symbol: %v", err)
	}
	if !slices.Contains(out.Files["example.com/sandbox/shapes"], "shapes.go") {
		t.Errorf("delete echo missing the touched file: %+v", out)
	}

	_, noop, err := deleteSymbol(st, testCfg())(context.Background(), nil, DeleteSymbolInput{
		Deletes: []DeleteSymbolEntry{{PkgPath: "shapes", SymbolKey: "Circle"}},
	})
	if err != nil {
		t.Fatalf("delete_symbol (already gone): %v", err)
	}
	if len(noop.Files) != 0 {
		t.Errorf("deleting an already-gone symbol must be a noop, got %+v", noop)
	}

	_, fileNoop, err := deleteFile(st, testCfg())(context.Background(), nil, DeleteFileInput{
		Deletes: []DeleteFileEntry{{PkgPath: "shapes", FileName: "nosuch.go"}},
	})
	if err != nil {
		t.Fatalf("delete_file (absent): %v", err)
	}
	if len(fileNoop.Files) != 0 {
		t.Errorf("deleting a nonexistent file must be a noop, got %+v", fileNoop)
	}

	_, pkgNoop, err := deletePackage(st, testCfg())(context.Background(), nil, DeletePackageInput{
		Deletes: []DeletePackageEntry{{PkgPath: "nosuchpkg"}},
	})
	if err != nil {
		t.Fatalf("delete_package (absent): %v", err)
	}
	if len(pkgNoop.Files) != 0 {
		t.Errorf("deleting a nonexistent package must be a noop, got %+v", pkgNoop)
	}
}
