package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestDescribeFileBatch(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{
			{PkgPath: "shapes", FileName: "shapes.go"},
			{PkgPath: "shapes", FileName: "groups.go"},
		},
	})
	if err != nil {
		t.Fatalf("describe_file batch: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("Results = %d entries, want 2", len(out.Results))
	}
	if out.Results[0].Doc == nil || *out.Results[0].Doc != "Package shapes provides fixture shape types for tests." {
		t.Errorf("Results[0].Doc = %v", out.Results[0].Doc)
	}

	if _, _, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{
			{PkgPath: "shapes", FileName: "shapes.go"},
			{PkgPath: "shapes", FileName: "nope.go"},
		},
	}); err == nil || !strings.Contains(err.Error(), "describes[1]") {
		t.Errorf("expected describes[1] to fail on the missing file, got %v", err)
	}
}

func TestDescribePackageBatch(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := describePackage(st, testCfg())(context.Background(), nil, DescribePackageInput{
		Describes: []DescribePackageEntry{
			{PkgPath: "shapes"},
			{PkgPath: "use"},
		},
	})
	if err != nil {
		t.Fatalf("describe_package batch: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("Results = %d entries, want 2", len(out.Results))
	}
	if !slices.ContainsFunc(out.Results[0].Files, func(f FileEntry) bool { return f.Name == "shapes.go" }) {
		t.Errorf("Results[0].Files missing shapes.go: %v", out.Results[0].Files)
	}

	if _, _, err := describePackage(st, testCfg())(context.Background(), nil, DescribePackageInput{
		Describes: []DescribePackageEntry{
			{PkgPath: "shapes"},
			{PkgPath: "nope"},
		},
	}); err == nil || !strings.Contains(err.Error(), "describes[1]") {
		t.Errorf("expected describes[1] to fail on the missing package, got %v", err)
	}
}

func TestDescribeSymbolBatch(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "Circle", FileName: "shapes.go"},
			{PkgPath: "shapes", SymbolKey: "Square", FileName: "shapes.go"},
		},
	})
	if err != nil {
		t.Fatalf("describe_symbol batch: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("Results = %d entries, want 2", len(out.Results))
	}
	if !strings.Contains(out.Results[0].Source, "type Circle struct") {
		t.Errorf("Results[0] wrong: %q", out.Results[0].Source)
	}
	if !strings.Contains(out.Results[1].Source, "type Square struct") {
		t.Errorf("Results[1] wrong: %q", out.Results[1].Source)
	}

	if _, _, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "Circle", FileName: "shapes.go"},
			{PkgPath: "shapes", SymbolKey: "Nope", FileName: "shapes.go"},
		},
	}); err == nil || !strings.Contains(err.Error(), "describes[1]") {
		t.Errorf("expected describes[1] to fail on the missing symbol, got %v", err)
	}
}

func TestDescribeSymbolEveryKind(t *testing.T) {
	st := sandboxStore(t)

	_, out, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Circle", FileName: "shapes.go"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(Circle): %v", err)
	}
	typ := out.Results[0]
	if !strings.Contains(typ.Source, "type Circle struct") || typ.Kind != "type" {
		t.Errorf("describe_symbol(Circle) wrong: kind=%s", typ.Kind)
	}
	if !slices.ContainsFunc(typ.Methods, func(s string) bool {
		return strings.Contains(s, "Area() float64")
	}) {
		t.Errorf("describe_symbol(Circle) missing Area: %v", typ.Methods)
	}

	_, out, err = describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "use", SymbolKey: "NewCircle", FileName: "use.go"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(NewCircle): %v", err)
	}
	fn := out.Results[0]
	if !strings.Contains(fn.Source, "func NewCircle(") || fn.Kind != "func" {
		t.Errorf("describe_symbol(NewCircle) wrong: kind=%s", fn.Kind)
	}

	_, out, err = describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Circle.Area", FileName: "shapes.go"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(Circle.Area): %v", err)
	}
	m := out.Results[0]
	if !strings.Contains(m.Source, "func (c Circle) Area()") || m.Kind != "method" {
		t.Errorf("describe_symbol(Circle.Area) wrong: kind=%s", m.Kind)
	}

	// The point of consolidating: var/const now have a describe path too,
	// which describe_type/describe_function/describe_method never gave them.
	_, out, err = describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "DefaultScale", FileName: "groups.go"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(DefaultScale): %v", err)
	}
	v := out.Results[0]
	if v.Kind != "var" || !strings.Contains(v.Source, "DefaultScale") {
		t.Errorf("describe_symbol(DefaultScale) wrong: kind=%s source=%q", v.Kind, v.Source)
	}

	_, out, err = describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "KindCircle", FileName: "groups.go"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(KindCircle): %v", err)
	}
	c := out.Results[0]
	if c.Kind != "const" || !strings.Contains(c.Source, "KindCircle") {
		t.Errorf("describe_symbol(KindCircle) wrong: kind=%s source=%q", c.Kind, c.Source)
	}

	if _, _, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Nope", FileName: "shapes.go"}},
	}); err == nil {
		t.Error("describing a missing symbol must error")
	}
}

func TestPackageDocTools(t *testing.T) {
	st := sandboxStore(t)

	_, out, err := describePackage(st, testCfg())(context.Background(), nil, DescribePackageInput{
		Describes: []DescribePackageEntry{{PkgPath: "shapes"}},
	})
	if err != nil {
		t.Fatalf("describe_package: %v", err)
	}
	desc := out.Results[0]
	want := "Kinds are grouped separately from shapes themselves.\n\nPackage shapes provides fixture shape types for tests."
	if desc.Doc == nil || *desc.Doc != want {
		t.Errorf("describe_package(shapes).Doc = %v, want %q", desc.Doc, want)
	}
	if !slices.ContainsFunc(desc.Files, func(f FileEntry) bool { return f.Name == "shapes.go" }) ||
		!slices.ContainsFunc(desc.Files, func(f FileEntry) bool { return f.Name == "groups.go" }) {
		t.Errorf("describe_package(shapes).Files missing entries: %v", desc.Files)
	}

	_, fout, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "shapes.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file: %v", err)
	}
	file := fout.Results[0]
	if file.Doc == nil || *file.Doc != "Package shapes provides fixture shape types for tests." {
		t.Errorf("describe_file(shapes.go).Doc = %v", file.Doc)
	}

	_, created, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "new_doc.go", Doc: new("New file doc.")}},
	})
	if err != nil {
		t.Fatalf("create_file: %v", err)
	}
	if created.IntroducedDiagnostics != nil {
		t.Errorf("create_file with doc produced diagnostics: %+v", created)
	}
	_, fout2, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "new_doc.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file(new_doc.go): %v", err)
	}
	newFile := fout2.Results[0]
	if newFile.Doc == nil || *newFile.Doc != "New file doc." {
		t.Errorf("new_doc.go doc = %v, want %q", newFile.Doc, "New file doc.")
	}

	if _, _, err := editFile(st, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "new_doc.go", Doc: new("")}},
	}); err != nil {
		t.Fatalf("edit_file (clear): %v", err)
	}
	_, fout3, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "new_doc.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file(new_doc.go) after clear: %v", err)
	}
	cleared := fout3.Results[0]
	if cleared.Doc != nil {
		t.Errorf("cleared doc = %v, want nil", cleared.Doc)
	}

	if _, _, err := editFile(st, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "nope.go", Doc: new("x")}},
	}); err == nil {
		t.Error("edit_file on a missing file must error")
	}
}

func TestDescribePackageExcludedFiles(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := describePackage(st, testCfg())(context.Background(), nil, DescribePackageInput{
		Describes: []DescribePackageEntry{{PkgPath: "shapes"}},
	})
	if err != nil {
		t.Fatalf("describe_package: %v", err)
	}
	// never_built.go is loaded like any other file (its own Ignored bit is
	// what marks it build-excluded, not a separate merged/inert list) — so
	// it legitimately appears in the ordinary Files list, correctly marked.
	entry, ok := findFileEntry(out.Results[0].Files, "never_built.go")
	if !ok {
		t.Fatalf("Files = %v, want never_built.go — it's loaded like any other file", out.Results[0].Files)
	}
	if !entry.Ignored {
		t.Errorf("never_built.go entry = %+v, want Ignored = true", entry)
	}
}

func TestDescribeSymbolDirectives(t *testing.T) {
	st := sandboxStore(t)
	if _, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "noinline.go"}},
	}); err != nil {
		t.Fatalf("create_file: %v", err)
	}
	_, _, err := createSymbol(st, testCfg())(context.Background(), nil, CreateSymbolInput{
		Creates: []CreateSymbolEntry{{
			PkgPath: "shapes", FileName: "noinline.go",
			Source: "// Noinline never gets inlined.\n//go:noinline\nfunc Noinline() {}",
		}},
	})
	if err != nil {
		t.Fatalf("create_symbol: %v", err)
	}
	_, out, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Noinline", FileName: "noinline.go"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol: %v", err)
	}
	if !slices.Equal(out.Results[0].Directives, []string{"go:noinline"}) {
		t.Errorf("Directives = %v, want [go:noinline]", out.Results[0].Directives)
	}
}

// TestDescribeSymbolReportsIgnoredPackageKind confirms a symbol declared
// in a build-tag-excluded file (never_built.go, //go:build ignore) is
// resolvable through the ordinary describe_symbols address — no suffix,
// same canonical package as its Prod sibling — and that it's reported as
// Ignored (a file-level fact, orthogonal to PackageKind, which stays
// empty since the file's own shape is still Prod).
func TestDescribeSymbolReportsIgnoredPackageKind(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "NeverBuilt", FileName: "never_built.go"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol: %v", err)
	}
	if out.Results[0].PackageKind != "" {
		t.Errorf("PackageKind = %q, want empty (Ignored is orthogonal to shape)", out.Results[0].PackageKind)
	}
	if !out.Results[0].Ignored {
		t.Error("Ignored = false, want true for a symbol in a //go:build ignore file")
	}
}
