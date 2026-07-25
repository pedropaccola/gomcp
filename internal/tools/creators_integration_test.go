package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestCreateFileBatchAbortsWhollyOnFailure(t *testing.T) {
	eng := sandboxEngine(t)
	_, _, err := createFile(eng, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{
			{PkgPath: "shapes", FileName: "first.go"},
			{PkgPath: "shapes", FileName: "shapes.go"}, // already exists
		},
	})
	if err == nil {
		t.Fatal("batch with a colliding entry must fail")
	}
	if !strings.Contains(err.Error(), "creates[1]") {
		t.Errorf("error must name the failing entry, got %v", err)
	}
	_, out, err := listFiles(eng, testCfg())(context.Background(), nil, ListFilesInput{PkgPath: "shapes"})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if slices.Contains(out.Files, "first.go") {
		t.Errorf("Error must mean untouched: first.go exists despite the batch failing, got %v", out.Files)
	}
}

func TestCreatePackageBatch(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := createPackage(eng, testCfg())(context.Background(), nil, CreatePackageInput{
		Creates: []CreatePackageEntry{
			{PkgPath: "widgets"},
			{PkgPath: "gadgets"},
		},
	})
	if err != nil {
		t.Fatalf("create_package batch: %v", err)
	}
	if !slices.Contains(out.Files["example.com/sandbox/widgets"], "widgets.go") ||
		!slices.Contains(out.Files["example.com/sandbox/gadgets"], "gadgets.go") {
		t.Errorf("batch echo missing an entry's file: %+v", out)
	}
}

func TestCreateSymbolBatchAbortsWhollyOnFailure(t *testing.T) {
	eng := sandboxEngine(t)
	_, _, err := createSymbol(eng, testCfg())(context.Background(), nil, CreateSymbolInput{
		Creates: []CreateSymbolEntry{
			{PkgPath: "shapes", FileName: "batch.go", Source: "func Foo() {}"},
			{PkgPath: "shapes", FileName: "batch.go", Source: "func Foo() {}"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "creates[1]") {
		t.Fatalf("expected creates[1] to fail on the duplicate, got %v", err)
	}
	if _, _, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Foo"}},
	}); err == nil {
		t.Error("Foo must not exist — the whole batch should have been discarded, including entry 0")
	}
}

func TestCreateSymbolBatchRefusesEmpty(t *testing.T) {
	eng := sandboxEngine(t)
	if _, _, err := createSymbol(eng, testCfg())(context.Background(), nil, CreateSymbolInput{}); err == nil {
		t.Error("an empty batch must be refused")
	}
}

func TestCreateSymbolMultiEntry(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := createSymbol(eng, testCfg())(context.Background(), nil, CreateSymbolInput{
		Creates: []CreateSymbolEntry{
			{PkgPath: "shapes", FileName: "batch.go", Source: "func Foo() {}"},
			{PkgPath: "shapes", FileName: "batch.go", Source: "func Bar() {}"},
		},
	})
	if err != nil {
		t.Fatalf("create_symbol: %v", err)
	}
	if out.IntroducedDiagnostics != nil {
		t.Errorf("batch introduced diagnostics: %+v", out)
	}
	if _, _, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Foo"}},
	}); err != nil {
		t.Errorf("Foo missing after batch: %v", err)
	}
	if _, _, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Bar"}},
	}); err != nil {
		t.Errorf("Bar missing after batch: %v", err)
	}
}
