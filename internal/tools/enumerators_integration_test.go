package tools

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestListPackages(t *testing.T) {
	st := sandboxStore(t)
	_, out, err := listPackages(st, testCfg())(context.Background(), nil, ListPackagesInput{})
	if err != nil {
		t.Fatalf("list_packages: %v", err)
	}
	for _, want := range []string{
		"example.com/sandbox/broken",
		"example.com/sandbox/shapes",
		"example.com/sandbox/use",
	} {
		if !slices.Contains(out.Packages, want) {
			t.Errorf("list_packages missing %q: %v", want, out.Packages)
		}
	}
	if !slices.IsSorted(out.Packages) {
		t.Error("list_packages output not sorted")
	}
}

func TestListSymbolsAndFiles(t *testing.T) {
	st := sandboxStore(t)

	_, files, err := listFiles(st, testCfg())(context.Background(), nil, ListFilesInput{PkgPath: "shapes"})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if !slices.Contains(files.Files, "groups.go") {
		t.Errorf("list_files missing groups.go: %v", files.Files)
	}

	_, syms, err := listSymbols(st, testCfg())(context.Background(), nil, ListSymbolsInput{
		PkgPath:  "shapes",
		FileName: new("groups.go"),
	})
	if err != nil {
		t.Fatalf("list_symbols: %v", err)
	}
	var kindCircle *SymbolEntry
	for i, s := range syms.Symbols {
		if s.SymbolKey == "KindCircle" {
			kindCircle = &syms.Symbols[i]
		}
		if s.SymbolKey == "Circle" {
			t.Error("file filter leaked a symbol from shapes.go")
		}
	}
	if kindCircle == nil || kindCircle.Kind != "const" || !strings.Contains(kindCircle.Summary, "Kind = iota") {
		t.Errorf("KindCircle entry wrong: %+v", kindCircle)
	}

	if _, _, err := listSymbols(st, testCfg())(context.Background(), nil, ListSymbolsInput{PkgPath: "no/such/pkg"}); err == nil {
		t.Error("list_symbols on a missing package must error")
	}
}
