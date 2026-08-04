package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestEditFileBatch(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := editFile(st, testCfg())(context.Background(), nil, EditFileInput{
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
	st := sandboxStore(t)
	_, _, err := editFile(st, testCfg())(context.Background(), nil, EditFileInput{
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
	st := sandboxStore(t)
	_, _, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "NotShape", FileName: "shapes.go", Source: "type NotShape struct{ X int }"},
			{PkgPath: "shapes", SymbolKey: "Missing", FileName: "shapes.go", Source: "type Missing struct{}"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "edits[1]") {
		t.Fatalf("expected edits[1] to fail on the missing symbol, got %v", err)
	}
	_, out, derr := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "NotShape", FileName: "shapes.go"}},
	})
	if derr != nil {
		t.Fatalf("describe_symbol: %v", derr)
	}
	if strings.Contains(out.Results[0].Source, "X int") {
		t.Errorf("NotShape must be untouched — the whole batch should have been discarded: %q", out.Results[0].Source)
	}
}

func TestEditSymbolBatchRefusesDuplicateTarget(t *testing.T) {
	st := sandboxStore(t)
	_, _, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "NotShape", FileName: "shapes.go", Source: "type NotShape struct{ X int }"},
			{PkgPath: "shapes", SymbolKey: "NotShape", FileName: "shapes.go", Source: "type NotShape struct{ Y int }"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate target") {
		t.Fatalf("expected a duplicate-target refusal, got %v", err)
	}
	_, out, derr := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "NotShape", FileName: "shapes.go"}},
	})
	if derr != nil {
		t.Fatalf("describe_symbol: %v", derr)
	}
	if strings.Contains(out.Results[0].Source, "X int") || strings.Contains(out.Results[0].Source, "Y int") {
		t.Errorf("NotShape must be untouched after a refused batch: %q", out.Results[0].Source)
	}
}

func TestEditSymbolBatchRefusesEmpty(t *testing.T) {
	st := sandboxStore(t)
	if _, _, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{}); err == nil {
		t.Error("an empty batch must be refused")
	}
}

func TestEditSymbolMultiEntry(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "NotShape", FileName: "shapes.go", Source: "type NotShape struct{ X int }"},
			{PkgPath: "shapes", SymbolKey: "DefaultScale", FileName: "groups.go", Source: "// DefaultScale stretches everything.\nDefaultScale = 2.0"},
		},
	})
	if err != nil {
		t.Fatalf("edit_symbol: %v", err)
	}
	if out.IntroducedDiagnostics != nil {
		t.Errorf("batch introduced diagnostics: %+v", out)
	}
	_, ns, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "NotShape", FileName: "shapes.go"}},
	})
	if err != nil || !strings.Contains(ns.Results[0].Source, "X int") {
		t.Errorf("NotShape not updated: %v %q", err, ns.Results[0].Source)
	}
	_, ds, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "DefaultScale", FileName: "groups.go"}},
	})
	if err != nil || !strings.Contains(ds.Results[0].Source, "2.0") {
		t.Errorf("DefaultScale not updated: %v %q", err, ds.Results[0].Source)
	}
}

func TestMutationTools(t *testing.T) {
	st := sandboxStore(t)

	if _, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "extra.go"}},
	}); err != nil {
		t.Fatalf("create_file: %v", err)
	}
	_, created, err := createSymbol(st, testCfg())(context.Background(), nil, CreateSymbolInput{
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

	_, edited, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{
			PkgPath: "shapes", SymbolKey: "Circle", FileName: "shapes.go",
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

	_, healed, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{
			PkgPath: "shapes", SymbolKey: "Circle", FileName: "shapes.go",
			Source: "type Circle struct{ R float64 }",
		}},
	})
	if err != nil {
		t.Fatalf("edit_symbol (heal): %v", err)
	}
	if healed.ResolvedDiagnostics == nil || healed.IntroducedDiagnostics != nil {
		t.Errorf("healing echo must report resolved and nothing introduced: %+v", healed)
	}

	if _, _, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{PkgPath: "shapes", SymbolKey: "Nope", FileName: "shapes.go", Source: "func Nope() {}"}},
	}); err == nil {
		t.Error("editing a missing symbol must error")
	}
}

func TestEditFileDirectivesIndependentOfDoc(t *testing.T) {
	st := sandboxStore(t)
	_, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{
			PkgPath: "shapes", FileName: "gen2.go", Doc: new("Original doc."),
			Directives: []string{"go:build linux"},
		}},
	})
	if err != nil {
		t.Fatalf("create_file: %v", err)
	}

	// Edit doc only — omitted directives field must leave the directive
	// block untouched.
	_, _, err = editFile(st, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "gen2.go", Doc: new("Updated doc.")}},
	})
	if err != nil {
		t.Fatalf("edit_file (doc only): %v", err)
	}
	_, out, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "gen2.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file: %v", err)
	}
	if out.Results[0].Doc == nil || *out.Results[0].Doc != "Updated doc." {
		t.Errorf("Doc = %v, want %q", out.Results[0].Doc, "Updated doc.")
	}
	if !slices.Equal(out.Results[0].Directives, []string{"go:build linux"}) {
		t.Errorf("Directives = %v, want untouched [go:build linux]", out.Results[0].Directives)
	}
}

// TestEditIgnoredSymbolWritesToIgnoredMap confirms editing a symbol
// declared in an Ignored file (never_built.go) writes back correctly —
// and doesn't disturb the Prod sibling's own declarations.
func TestEditIgnoredSymbolWritesToIgnoredMap(t *testing.T) {
	st := sandboxStore(t)
	_, _, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{
			PkgPath: "shapes", SymbolKey: "NeverBuilt", FileName: "never_built.go",
			Source: "func NeverBuilt() { _ = 1 }",
		}},
	})
	if err != nil {
		t.Fatalf("edit_symbol: %v", err)
	}
	_, out, err := describeSymbol(st, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "NeverBuilt", FileName: "never_built.go"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol after edit: %v", err)
	}
	if !strings.Contains(out.Results[0].Source, "_ = 1") {
		t.Errorf("Source = %q, edit did not apply", out.Results[0].Source)
	}
	if !out.Results[0].Ignored {
		t.Error("Ignored = false, edit must not have moved the symbol out of its Ignored file")
	}
	_, desc, err := describePackage(st, testCfg())(context.Background(), nil, DescribePackageInput{
		Describes: []DescribePackageEntry{{PkgPath: "shapes"}},
	})
	if err != nil {
		t.Fatalf("describe_package: %v", err)
	}
	if !slices.ContainsFunc(desc.Results[0].Files, func(f FileEntry) bool { return f.Name == "shapes.go" }) {
		t.Error("editing the Ignored file must not disturb the Prod sibling's own files")
	}
}

// TestEditFileDirectiveTogglesReclassification confirms the eager
// reclassification path: editing a plain file's directives to add an
// excluding //go:build tag marks it Ignored in the same transaction,
// and clearing that directive clears it again — no flush/reload round
// trip needed either way. Shape (PackageKind) never changes: it's the
// file's own Ignored bit that flips.
func TestEditFileDirectiveTogglesReclassification(t *testing.T) {
	st := sandboxStore(t)
	if _, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "toggle.go"}},
	}); err != nil {
		t.Fatalf("create_file: %v", err)
	}

	if _, _, err := editFile(st, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "toggle.go", Directives: []string{"go:build ignore"}}},
	}); err != nil {
		t.Fatalf("edit_file (add excluding directive): %v", err)
	}
	_, desc, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "toggle.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file after excluding: %v", err)
	}
	if !desc.Results[0].Ignored {
		t.Error("Ignored = false, want true after adding an excluding directive")
	}

	if _, _, err := editFile(st, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "toggle.go", Directives: []string{}}},
	}); err != nil {
		t.Fatalf("edit_file (clear directive): %v", err)
	}
	_, desc2, err := describeFile(st, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "toggle.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file after clearing: %v", err)
	}
	if desc2.Results[0].Ignored {
		t.Error("Ignored = true, want false after clearing the directive")
	}
}

// TestEditFileDirectivesReportsAddedAndRemoved confirms edit_file's
// WriteOutput surfaces a DirectiveChange whenever an explicit directives
// value actually changes the file's directive set — added going from
// none to one, removed going from one back to none — scoped to the file
// (no SymbolKey), never on the create that seeded the file in the first
// place.
func TestEditFileDirectivesReportsAddedAndRemoved(t *testing.T) {
	st := sandboxStore(t)
	if _, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "drift.go"}},
	}); err != nil {
		t.Fatalf("create_file: %v", err)
	}

	_, out, err := editFile(st, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "drift.go", Directives: []string{"go:build linux"}}},
	})
	if err != nil {
		t.Fatalf("edit_file (add): %v", err)
	}
	if len(out.DirectiveChanges) != 1 {
		t.Fatalf("DirectiveChanges = %+v, want exactly one entry", out.DirectiveChanges)
	}
	change := out.DirectiveChanges[0]
	if change.FileName != "drift.go" || change.SymbolKey != "" {
		t.Errorf("change scoping = %+v, want file=drift.go symbol=\"\"", change)
	}
	if !slices.Equal(change.Added, []string{"go:build linux"}) || len(change.Removed) != 0 {
		t.Errorf("Added/Removed = %v/%v, want [go:build linux]/[]", change.Added, change.Removed)
	}

	_, out2, err := editFile(st, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "drift.go", Directives: []string{}}},
	})
	if err != nil {
		t.Fatalf("edit_file (clear): %v", err)
	}
	if len(out2.DirectiveChanges) != 1 {
		t.Fatalf("DirectiveChanges = %+v, want exactly one entry", out2.DirectiveChanges)
	}
	change2 := out2.DirectiveChanges[0]
	if !slices.Equal(change2.Removed, []string{"go:build linux"}) || len(change2.Added) != 0 {
		t.Errorf("Added/Removed = %v/%v, want []/[go:build linux]", change2.Added, change2.Removed)
	}
}

// TestEditSymbolDirectivesReportsAddedAndRemoved confirms edit_symbol's
// WriteOutput surfaces a DirectiveChange whenever a replacement's own
// comment block changes which directive-shaped lines it carries —
// detected automatically from Source, with no separate declaration
// needed — scoped to the symbol (SymbolKey present), never on create.
func TestEditSymbolDirectivesReportsAddedAndRemoved(t *testing.T) {
	st := sandboxStore(t)
	if _, _, err := createFile(st, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "symdrift.go"}},
	}); err != nil {
		t.Fatalf("create_file: %v", err)
	}
	if _, out, err := createSymbol(st, testCfg())(context.Background(), nil, CreateSymbolInput{
		Creates: []CreateSymbolEntry{{PkgPath: "shapes", FileName: "symdrift.go", Source: "func Gen() {}"}},
	}); err != nil {
		t.Fatalf("create_symbol: %v", err)
	} else if len(out.DirectiveChanges) != 0 {
		t.Errorf("create_symbol reported DirectiveChanges = %+v, want none", out.DirectiveChanges)
	}

	_, out, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{PkgPath: "shapes", SymbolKey: "Gen", FileName: "symdrift.go", Source: "//go:generate mockgen -source=symdrift.go\nfunc Gen() {}"}},
	})
	if err != nil {
		t.Fatalf("edit_symbol (add): %v", err)
	}
	if len(out.DirectiveChanges) != 1 {
		t.Fatalf("DirectiveChanges = %+v, want exactly one entry", out.DirectiveChanges)
	}
	change := out.DirectiveChanges[0]
	if change.FileName != "symdrift.go" || change.SymbolKey != "Gen" {
		t.Errorf("change scoping = %+v, want file=symdrift.go symbol=Gen", change)
	}
	want := []string{"go:generate mockgen -source=symdrift.go"}
	if !slices.Equal(change.Added, want) || len(change.Removed) != 0 {
		t.Errorf("Added/Removed = %v/%v, want %v/[]", change.Added, change.Removed, want)
	}

	_, out2, err := editSymbol(st, testCfg())(context.Background(), nil, EditSymbolInput{
		Edits: []EditSymbolEntry{{PkgPath: "shapes", SymbolKey: "Gen", FileName: "symdrift.go", Source: "func Gen() {}"}},
	})
	if err != nil {
		t.Fatalf("edit_symbol (remove): %v", err)
	}
	if len(out2.DirectiveChanges) != 1 {
		t.Fatalf("DirectiveChanges = %+v, want exactly one entry", out2.DirectiveChanges)
	}
	change2 := out2.DirectiveChanges[0]
	if !slices.Equal(change2.Removed, want) || len(change2.Added) != 0 {
		t.Errorf("Added/Removed = %v/%v, want []/%v", change2.Added, change2.Removed, want)
	}
}
