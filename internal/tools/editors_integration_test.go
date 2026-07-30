package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestEditFileBatch(t *testing.T) {
	eng := sandboxStore(t)
	_, out, err := editFile(eng, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{
			{PkgPath: "shapes", FileName: "shapes.go", Doc: new("Updated shapes doc.")},
			{PkgPath: "use", FileName: "use.go", Doc: new("Updated use doc.")},
		},
	})
	if err != nil {
		t.Fatalf("edit_file batch: %v", err)
	}
	if !slices.Contains(out.Files["example.com/sandbox/shapes"], "shapes.go") ||
		!slices.Contains(out.Files["example.com/sandbox/use"], "use.go") {
		t.Errorf("batch echo missing an entry's file: %+v", out)
	}
}

func TestEditFileBatchRefusesDuplicateTarget(t *testing.T) {
	eng := sandboxStore(t)
	_, _, err := editFile(eng, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{
			{PkgPath: "shapes", FileName: "shapes.go", Doc: new("First.")},
			{PkgPath: "shapes", FileName: "shapes.go", Doc: new("Second.")},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate target") {
		t.Errorf("duplicate target must be refused before the transaction opens, got %v", err)
	}
}

func TestEditSymbolBatchAbortsWhollyOnFailure(t *testing.T) {
	eng := sandboxStore(t)
	_, _, err := editSymbol(eng, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "NotShape", Source: "type NotShape struct{ X int }"},
			{PkgPath: "shapes", SymbolKey: "Missing", Source: "type Missing struct{}"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "edits[1]") {
		t.Fatalf("expected edits[1] to fail on the missing symbol, got %v", err)
	}
	_, out, derr := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "NotShape"}},
	})
	if derr != nil || strings.Contains(out.Results[0].Source, "X int") {
		t.Errorf("NotShape must be untouched — the whole batch should have been discarded: %v %q", derr, out.Results[0].Source)
	}
}

func TestEditSymbolBatchRefusesDuplicateTarget(t *testing.T) {
	eng := sandboxStore(t)
	_, _, err := editSymbol(eng, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "NotShape", Source: "type NotShape struct{ X int }"},
			{PkgPath: "shapes", SymbolKey: "NotShape", Source: "type NotShape struct{ Y int }"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate target") {
		t.Fatalf("expected a duplicate-target refusal, got %v", err)
	}
	_, out, derr := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "NotShape"}},
	})
	if derr != nil || strings.Contains(out.Results[0].Source, "X int") || strings.Contains(out.Results[0].Source, "Y int") {
		t.Errorf("NotShape must be untouched after a refused batch: %v %q", derr, out.Results[0].Source)
	}
}

func TestEditSymbolBatchRefusesEmpty(t *testing.T) {
	eng := sandboxStore(t)
	if _, _, err := editSymbol(eng, testCfg())(context.Background(), nil, EditSymbolInput{}); err == nil {
		t.Error("an empty batch must be refused")
	}
}

func TestEditSymbolMultiEntry(t *testing.T) {
	eng := sandboxStore(t)
	_, out, err := editSymbol(eng, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "NotShape", Source: "type NotShape struct{ X int }"},
			{PkgPath: "shapes", SymbolKey: "DefaultScale", Source: "// DefaultScale stretches everything.\nDefaultScale = 2.0"},
		},
	})
	if err != nil {
		t.Fatalf("edit_symbol: %v", err)
	}
	if out.IntroducedDiagnostics != nil {
		t.Errorf("batch introduced diagnostics: %+v", out)
	}
	_, ns, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "NotShape"}},
	})
	if err != nil || !strings.Contains(ns.Results[0].Source, "X int") {
		t.Errorf("NotShape not updated: %v %q", err, ns.Results[0].Source)
	}
	_, ds, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "DefaultScale"}},
	})
	if err != nil || !strings.Contains(ds.Results[0].Source, "2.0") {
		t.Errorf("DefaultScale not updated: %v %q", err, ds.Results[0].Source)
	}
}

func TestMutationTools(t *testing.T) {
	eng := sandboxStore(t)

	_, created, err := createSymbol(eng, testCfg())(context.Background(), nil, CreateSymbolInput{
		Creates: []CreateSymbolEntry{{
			PkgPath: "shapes", FileName: "extra.go",
			Source: "func Twice(x float64) float64 { return 2 * x }",
		}},
	})
	if err != nil {
		t.Fatalf("create_symbol: %v", err)
	}
	if !slices.Contains(created.Files["example.com/sandbox/shapes"], "extra.go") || created.IntroducedDiagnostics != nil {
		t.Errorf("create echo wrong: %+v", created)
	}

	_, edited, err := editSymbol(eng, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{
			PkgPath: "shapes", SymbolKey: "Circle",
			Source: "type Circle struct{ Radius float64 }",
		}},
	})
	if err != nil {
		t.Fatalf("edit_symbol: %v", err)
	}
	if edited.IntroducedDiagnostics == nil || !slices.ContainsFunc(edited.IntroducedDiagnostics.Diagnostics, func(d DiagnosticEntry) bool {
		return d.FileName == "use.go"
	}) {
		t.Errorf("edit echo missing the blast radius in use/use.go: %+v", edited)
	}

	_, healed, err := editSymbol(eng, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{
			PkgPath: "shapes", SymbolKey: "Circle",
			Source: "type Circle struct{ R float64 }",
		}},
	})
	if err != nil {
		t.Fatalf("edit_symbol (heal): %v", err)
	}
	if healed.ResolvedDiagnostics == nil || healed.IntroducedDiagnostics != nil {
		t.Errorf("healing echo must report resolved and nothing introduced: %+v", healed)
	}

	if _, _, err := editSymbol(eng, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{PkgPath: "shapes", SymbolKey: "Nope", Source: "func Nope() {}"}},
	}); err == nil {
		t.Error("editing a missing symbol must error")
	}
}
