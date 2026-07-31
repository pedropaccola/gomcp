package tools

import (
	"context"
	"strings"
	"testing"
)

func TestDiagnosticsPackagesBatch(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := diagnosticsPackages(st, testCfg())(context.Background(), nil, DiagnosticsPackagesInput{
		Diagnoses: []DiagnosticsPackageEntry{
			{PkgPath: "broken"},
			{PkgPath: "shapes"},
		},
	})
	if err != nil {
		t.Fatalf("diagnostics_packages batch: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("Results = %d entries, want 2", len(out.Results))
	}
	if len(out.Results[0].Diagnostics) == 0 {
		t.Errorf("Results[0] (broken) has no diagnostics, want at least one")
	}
	if len(out.Results[1].Diagnostics) != 0 {
		t.Errorf("Results[1] (shapes) has diagnostics, want none: %v", out.Results[1].Diagnostics)
	}

	if _, _, err := diagnosticsPackages(st, testCfg())(context.Background(), nil, DiagnosticsPackagesInput{
		Diagnoses: []DiagnosticsPackageEntry{
			{PkgPath: "shapes"},
			{PkgPath: "nope"},
		},
	}); err == nil || !strings.Contains(err.Error(), "diagnoses[1]") {
		t.Errorf("expected diagnoses[1] to fail on the missing package, got %v", err)
	}
}

func TestDiagnosticsFilesBatch(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := diagnosticsFiles(st, testCfg())(context.Background(), nil, DiagnosticsFilesInput{
		Diagnoses: []DiagnosticsFileEntry{
			{PkgPath: "broken", FileName: "broken.go"},
			{PkgPath: "shapes", FileName: "shapes.go"},
		},
	})
	if err != nil {
		t.Fatalf("diagnostics_files batch: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("Results = %d entries, want 2", len(out.Results))
	}
	if len(out.Results[0].Diagnostics) == 0 {
		t.Errorf("Results[0] (broken.go) has no diagnostics, want at least one")
	}
	if len(out.Results[1].Diagnostics) != 0 {
		t.Errorf("Results[1] (shapes.go) has diagnostics, want none: %v", out.Results[1].Diagnostics)
	}
}

// TestDiagnosticsSymbolsBatchAcrossPackages exercises the one capability
// diagnostics_packages/diagnostics_files don't have: naming symbols from
// different packages in a single batch, since each entry is already
// individually addressed.
func TestDiagnosticsSymbolsBatchAcrossPackages(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := diagnosticsSymbols(st, testCfg())(context.Background(), nil, DiagnosticsSymbolsInput{
		Diagnoses: []DiagnosticsSymbolEntry{
			{PkgPath: "broken", SymbolKey: "Bad"},
			{PkgPath: "shapes", SymbolKey: "Circle"},
		},
	})
	if err != nil {
		t.Fatalf("diagnostics_symbols batch: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("Results = %d entries, want 2", len(out.Results))
	}
	if len(out.Results[0].Diagnostics) == 0 {
		t.Errorf("Results[0] (broken.Bad) has no diagnostics, want at least one")
	}
	if len(out.Results[1].Diagnostics) != 0 {
		t.Errorf("Results[1] (shapes.Circle) has diagnostics, want none: %v", out.Results[1].Diagnostics)
	}
}
