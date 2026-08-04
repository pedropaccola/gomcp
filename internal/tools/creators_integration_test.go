package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestCreateFileBatchAbortsWhollyOnFailure(t *testing.T) {
	st := sandboxStore(t)
	_, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
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
	_, out, err := listFiles(st, testCfg())(context.Background(), nil, ListFilesInput{PkgPath: "shapes"})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if slices.ContainsFunc(out.Files, func(f FileEntry) bool { return f.Name == "first.go" }) {
		t.Errorf("Error must mean untouched: first.go exists despite the batch failing, got %v", out.Files)
	}
}

func TestCreatePackageBatch(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := createPackage(st, testCfg())(context.Background(), nil, CreatePackageInput{
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
	st := sandboxStore(t)
	if _, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "batch.go"}},
	}); err != nil {
		t.Fatalf("create_file: %v", err)
	}
	_, _, err := createSymbol(st, testCfg())(context.Background(), nil, CreateSymbolInput{
		Creates: []CreateSymbolEntry{
			{PkgPath: "shapes", FileName: "batch.go", Source: "func Foo() {}"},
			{PkgPath: "shapes", FileName: "batch.go", Source: "func Foo() {}"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "creates[1]") {
		t.Fatalf("expected creates[1] to fail on the duplicate, got %v", err)
	}
	if _, _, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Foo", FileName: "batch.go"}},
	}); err == nil {
		t.Error("Foo must not exist — the whole batch should have been discarded, including entry 0")
	}
}

func TestCreateSymbolBatchRefusesEmpty(t *testing.T) {
	st := sandboxStore(t)
	if _, _, err := createSymbol(st, testCfg())(context.Background(), nil, CreateSymbolInput{}); err == nil {
		t.Error("an empty batch must be refused")
	}
}

func TestCreateSymbolMultiEntry(t *testing.T) {
	st := sandboxStore(t)
	if _, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "batch.go"}},
	}); err != nil {
		t.Fatalf("create_file: %v", err)
	}
	_, out, err := createSymbol(st, testCfg())(context.Background(), nil, CreateSymbolInput{
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
	if _, _, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Foo", FileName: "batch.go"}},
	}); err != nil {
		t.Errorf("Foo missing after batch: %v", err)
	}
	if _, _, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Bar", FileName: "batch.go"}},
	}); err != nil {
		t.Errorf("Bar missing after batch: %v", err)
	}
}

// TestCreateFileXTestRequiresPackageFirst confirms create_file no longer
// implicitly originates a package or its XTest half: create_package with
// is_xtest must run first, in its own separate step — create a package,
// create a file, create a symbol, never overlapping.
func TestCreateFileXTestRequiresPackageFirst(t *testing.T) {
	st := sandboxStore(t)
	isXTest := true
	if _, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "brandnew", IsXTest: &isXTest, FileName: "extra_test.go"}},
	}); err == nil {
		t.Error("create_file must fail when the target XTest half doesn't exist yet, not originate it implicitly")
	}
	if _, _, err := createPackage(st, testCfg())(context.Background(), nil, CreatePackageInput{
		Creates: []CreatePackageEntry{{PkgPath: "brandnew", IsXTest: &isXTest}},
	}); err != nil {
		t.Fatalf("create_package(is_xtest): %v", err)
	}
	_, out, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "brandnew", IsXTest: &isXTest, FileName: "extra_test.go"}},
	})
	if err != nil {
		t.Fatalf("create_file: %v", err)
	}
	files := out.Files["example.com/sandbox/brandnew"]
	if !slices.Contains(files, "extra_test.go") {
		t.Errorf("requested XTest file missing: %+v", out)
	}
	_, desc, err := describePackage(st, testCfg())(context.Background(), nil, DescribePackageInput{
		Describes: []DescribePackageEntry{{PkgPath: "brandnew"}},
	})
	if err != nil {
		t.Fatalf("describe_package: %v", err)
	}
	if slices.ContainsFunc(desc.Results[0].Files, func(f FileEntry) bool { return f.Name == "brandnew.go" }) {
		t.Error("create_file(is_xtest) must not fabricate a Prod sibling stub")
	}
}

func TestCreateFileWithDirectivesRoundTrips(t *testing.T) {
	st := sandboxStore(t)
	_, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{
			PkgPath: "shapes", FileName: "gen.go", Doc: new("Gen holds a generated-shaped fixture."),
			Directives: []string{"go:build linux", "go:generate mockgen -source=gen.go"},
		}},
	})
	if err != nil {
		t.Fatalf("create_file: %v", err)
	}
	_, out, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "gen.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file: %v", err)
	}
	want := []string{"go:build linux", "go:generate mockgen -source=gen.go"}
	if !slices.Equal(out.Results[0].Directives, want) {
		t.Errorf("Directives = %v, want %v", out.Results[0].Directives, want)
	}
	if out.Results[0].Doc == nil || *out.Results[0].Doc != "Gen holds a generated-shaped fixture." {
		t.Errorf("Doc = %v", out.Results[0].Doc)
	}
}

// TestCreateFileWithExcludingDirectiveLandsInIgnored confirms the eager
// reclassification path on the create side: a brand-new file whose own
// //go:build directive excludes it from every build is marked Ignored in
// the same transaction that creates it — a file-level fact, independent
// of its shape, which stays the requested Prod half (PackageKind empty).
func TestCreateFileWithExcludingDirectiveLandsInIgnored(t *testing.T) {
	st := sandboxStore(t)
	_, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{
			PkgPath: "shapes", FileName: "reclassified.go",
			Directives: []string{"go:build ignore"},
		}},
	})
	if err != nil {
		t.Fatalf("create_file: %v", err)
	}
	_, out, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "reclassified.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file: %v", err)
	}
	if out.Results[0].PackageKind != "" {
		t.Errorf("PackageKind = %q, want empty (Ignored is orthogonal to shape)", out.Results[0].PackageKind)
	}
	if !out.Results[0].Ignored {
		t.Error("Ignored = false, want true for a file created with an excluding directive")
	}

	_, pkgOut, err := describePackage(st, testCfg())(context.Background(), nil, DescribePackageInput{
		Describes: []DescribePackageEntry{{PkgPath: "shapes"}},
	})
	if err != nil {
		t.Fatalf("describe_package: %v", err)
	}
	if !slices.ContainsFunc(pkgOut.Results[0].Files, func(f FileEntry) bool { return f.Name == "reclassified.go" }) {
		t.Errorf("describe_package Files = %v, want to include reclassified.go", pkgOut.Results[0].Files)
	}
}
