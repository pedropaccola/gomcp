package tools

import (
	"context"
	"strings"
	"testing"
)

func TestExternalReadToolsAndRefusals(t *testing.T) {
	eng := sandboxStore(t)

	_, out, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "io", SymbolKey: "Reader"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(io.Reader): %v", err)
	}
	typ := out.Results[0]
	if !strings.Contains(typ.Source, "type Reader interface") || typ.File != "io.go" {
		t.Errorf("describe_symbol(io.Reader) wrong: file=%s", typ.File)
	}

	_, syms, err := listSymbols(eng, testCfg())(context.Background(), nil, ListSymbolsInput{PkgPath: "io"})
	if err != nil {
		t.Fatalf("list_symbols(io): %v", err)
	}
	sawReader := false
	for _, s := range syms.Symbols {
		if s.SymbolKey == "Reader" {
			sawReader = true
		}
		if r := s.SymbolKey[0]; r >= 'a' && r <= 'z' {
			t.Errorf("unexported %q leaked out of a dependency", s.SymbolKey)
		}
	}
	if !sawReader {
		t.Error("list_symbols(io) missing Reader")
	}

	// The workspace is the only mutable world.
	if _, _, err := createSymbol(eng, testCfg())(context.Background(), nil, CreateSymbolInput{
		Creates: []CreateSymbolEntry{{PkgPath: "io", FileName: "extra.go", Source: "func Nope() {}"}},
	}); err == nil || !strings.Contains(err.Error(), "is a dependency") {
		t.Errorf("mutating a dependency must refuse, got %v", err)
	}

	// Semantic finders stay in the workspace.
	if _, _, err := searchReferences(eng)(context.Background(), nil, SearchReferencesInput{
		PkgPath: "io", SymbolKey: "Reader",
	}); err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Errorf("semantic search on a dependency must steer, got %v", err)
	}

	// A workspace typo still errors after the failed dependency attempt.
	if _, _, err := listFiles(eng, testCfg())(context.Background(), nil, ListFilesInput{PkgPath: "shaeps"}); err == nil {
		t.Error("typo'd address must error")
	}
}
