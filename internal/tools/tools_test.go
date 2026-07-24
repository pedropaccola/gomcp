package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

func moduleRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}

func sandboxEngine(tb testing.TB) *engine.Engine {
	tb.Helper()
	eng := engine.NewEngine(filepath.Join(moduleRoot(tb), "testdata", "sandbox"), nil)
	if err := eng.Bootstrap(context.Background()); err != nil {
		tb.Fatalf("Bootstrap: %v", err)
	}
	return eng
}

// TestRegister exercises schema generation for every declared tool shape —
// it panics or errors inside the SDK if a shape (e.g. the embedded DiagBlock)
// cannot be turned into a JSON schema.
func TestRegister(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(server, sandboxEngine(t), 20)
}

// TestToolAnnotations asserts the annotations exactly as a client sees them
// over the wire: the workspace is a closed world, reads are read-only, and
// only Creators are non-destructive among the mutators.
func TestToolAnnotations(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(server, sandboxEngine(t), 20)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools listed")
	}
	for _, tool := range tools.Tools {
		ann := tool.Annotations
		if ann == nil {
			t.Errorf("%s: no annotations", tool.Name)
			continue
		}
		if ann.Title == "" {
			t.Errorf("%s: missing title", tool.Name)
		}
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Errorf("%s: workspace is a closed world", tool.Name)
		}
		if !ann.IdempotentHint {
			t.Errorf("%s: retries never double-apply; must be idempotent", tool.Name)
		}
		isRead := strings.HasPrefix(tool.Name, "list_") || strings.HasPrefix(tool.Name, "describe_") ||
			strings.HasPrefix(tool.Name, "search_") || tool.Name == "diagnostics"
		if ann.ReadOnlyHint != isRead {
			t.Errorf("%s: ReadOnlyHint = %v", tool.Name, ann.ReadOnlyHint)
		}
		wantDestructive := !isRead && !strings.HasPrefix(tool.Name, "create_")
		if ann.DestructiveHint == nil || *ann.DestructiveHint != wantDestructive {
			t.Errorf("%s: DestructiveHint = %v, want %v", tool.Name, ann.DestructiveHint, wantDestructive)
		}
	}
}

func TestListPackages(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := listPackages(eng, testCfg())(context.Background(), nil, ListPackagesInput{})
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
	if len(out.Diagnostics) != 0 {
		t.Errorf("broken's type error is package-scoped, not workspace-scoped: %v", out.Diagnostics)
	}
}

func TestListSymbolsAndFiles(t *testing.T) {
	eng := sandboxEngine(t)

	_, files, err := listFiles(eng, testCfg())(context.Background(), nil, ListFilesInput{PkgPath: "shapes"})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if !slices.Contains(files.Files, "groups.go") {
		t.Errorf("list_files missing groups.go: %v", files.Files)
	}

	_, syms, err := listSymbols(eng, testCfg())(context.Background(), nil, ListSymbolsInput{
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

	if _, _, err := listSymbols(eng, testCfg())(context.Background(), nil, ListSymbolsInput{PkgPath: "no/such/pkg"}); err == nil {
		t.Error("list_symbols on a missing package must error")
	}
}

func TestDescribers(t *testing.T) {
	eng := sandboxEngine(t)

	_, out, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Circle"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(Circle): %v", err)
	}
	typ := out.Results[0]
	if !strings.Contains(typ.Source, "type Circle struct") || typ.File != "shapes.go" || typ.Kind != "type" {
		t.Errorf("describe_symbol(Circle) wrong: file=%s kind=%s", typ.File, typ.Kind)
	}
	if !slices.ContainsFunc(typ.Methods, func(s string) bool {
		return strings.Contains(s, "Area() float64")
	}) {
		t.Errorf("describe_symbol(Circle) missing Area: %v", typ.Methods)
	}

	_, out, err = describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "use", SymbolKey: "NewCircle"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(NewCircle): %v", err)
	}
	fn := out.Results[0]
	if !strings.Contains(fn.Source, "func NewCircle(") || fn.Kind != "func" {
		t.Errorf("describe_symbol(NewCircle) wrong: kind=%s", fn.Kind)
	}

	_, out, err = describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Circle.Area"}},
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
	_, out, err = describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "DefaultScale"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(DefaultScale): %v", err)
	}
	v := out.Results[0]
	if v.Kind != "var" || !strings.Contains(v.Source, "DefaultScale") {
		t.Errorf("describe_symbol(DefaultScale) wrong: kind=%s source=%q", v.Kind, v.Source)
	}

	_, out, err = describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "KindCircle"}},
	})
	if err != nil {
		t.Fatalf("describe_symbol(KindCircle): %v", err)
	}
	c := out.Results[0]
	if c.Kind != "const" || !strings.Contains(c.Source, "KindCircle") {
		t.Errorf("describe_symbol(KindCircle) wrong: kind=%s source=%q", c.Kind, c.Source)
	}

	if _, _, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{{PkgPath: "shapes", SymbolKey: "Nope"}},
	}); err == nil {
		t.Error("describing a missing symbol must error")
	}
}

func TestFindersAndDiagnostics(t *testing.T) {
	eng := sandboxEngine(t)

	_, like, err := searchDeclarationsLike(eng)(context.Background(), nil, SearchLikeInput{Name: "area"})
	if err != nil {
		t.Fatalf("search_declarations_like: %v", err)
	}
	if !slices.ContainsFunc(like.Matches, func(m MatchEntry) bool {
		return m.SymbolKey == "Circle.Area" && m.PkgPath == "example.com/sandbox/shapes" && m.Kind == "method"
	}) {
		t.Errorf("search_declarations_like(area) missing Circle.Area: %v", like.Matches)
	}

	_, src, err := searchSource(eng)(context.Background(), nil, SearchSourceInput{Regexp: `(?m)^type Embedded struct`})
	if err != nil {
		t.Fatalf("search_source: %v", err)
	}
	if len(src.Matches) != 1 || src.Matches[0].SymbolKey != "Embedded" {
		t.Errorf("search_source(type Embedded) = %v, want single Embedded", src.Matches)
	}
	if _, _, err := searchSource(eng)(context.Background(), nil, SearchSourceInput{Regexp: "("}); err == nil {
		t.Error("search_source must reject an invalid regexp")
	}

	_, diags, err := diagnostics(eng)(context.Background(), nil, DiagnosticsInput{})
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if !slices.ContainsFunc(diags.Diagnostics, func(d DiagnosticEntry) bool { return d.Kind == "type" }) {
		t.Errorf("sandbox type error missing from inventory: %v", diags.Diagnostics)
	}
}

func TestSemanticFinders(t *testing.T) {
	eng := sandboxEngine(t)

	_, impl, err := searchImplementors(eng)(context.Background(), nil, SearchImplementorsInput{
		PkgPath: "shapes", SymbolKey: "Shape",
	})
	if err != nil {
		t.Fatalf("search_implementors: %v", err)
	}
	if !slices.ContainsFunc(impl.Matches, func(m MatchEntry) bool { return m.SymbolKey == "Embedded" }) {
		t.Errorf("search_implementors(Shape) missing promoted-method implementor Embedded: %v", impl.Matches)
	}

	_, refs, err := searchReferences(eng)(context.Background(), nil, SearchReferencesInput{
		PkgPath: "shapes", SymbolKey: "Circle",
	})
	if err != nil {
		t.Fatalf("search_references: %v", err)
	}
	if !slices.ContainsFunc(refs.Matches, func(m MatchEntry) bool {
		return m.PkgPath == "example.com/sandbox/use" && m.SymbolKey == "NewCircle"
	}) {
		t.Errorf("search_references(Circle) missing use:NewCircle: %v", refs.Matches)
	}

	if _, _, err := searchImplementors(eng)(context.Background(), nil, SearchImplementorsInput{
		PkgPath: "shapes", SymbolKey: "Circle",
	}); err == nil || !strings.Contains(err.Error(), "interface") {
		t.Errorf("search_implementors on a struct must error mentioning interface, got %v", err)
	}
}

func TestMutationTools(t *testing.T) {
	eng := sandboxEngine(t)

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
		return d.FileName != nil && *d.FileName == "use/use.go"
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

func TestExternalReadToolsAndRefusals(t *testing.T) {
	eng := sandboxEngine(t)

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
	}); err == nil || !strings.Contains(err.Error(), "read-only") {
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

func TestDiagBlockTruncation(t *testing.T) {
	diags := make([]engine.Diagnostic, 5)
	for i := range diags {
		diags[i] = engine.Diagnostic{Kind: engine.DiagType, Msg: fmt.Sprintf("problem %d", i)}
	}

	cfg := newToolConfig(3)
	block := cfg.diagBlock(diags)
	if len(block.Diagnostics) != 3 {
		t.Fatalf("len(Diagnostics) = %d, want 3 shown", len(block.Diagnostics))
	}
	if block.Truncated == nil || *block.Truncated != 2 {
		t.Errorf("Truncated = %v, want 2", block.Truncated)
	}

	cfg = newToolConfig(10)
	if block := cfg.diagBlock(diags); len(block.Diagnostics) != 5 || block.Truncated != nil {
		t.Errorf("below the limit: %+v, want 5 shown, no truncation", block)
	}

	cfg = newToolConfig(0)
	if block := cfg.diagBlock(diags); len(block.Diagnostics) != 0 || block.Truncated == nil || *block.Truncated != 5 {
		t.Errorf("zero limit must still count everything as truncated: %+v", block)
	}

	cfg = newToolConfig(-1)
	if cfg.diagLimit != 20 {
		t.Errorf("newToolConfig must ignore negative n in favor of the default, got %d", cfg.diagLimit)
	}

	if block := cfg.diagBlock(nil); block.Diagnostics != nil || block.Truncated != nil {
		t.Errorf("empty input must stay a zero-value DiagBlock, got %+v", block)
	}
	if cfg.diagBlockPtr(nil) != nil {
		t.Error("diagBlockPtr must return nil for empty input")
	}
}

func TestPackageDocTools(t *testing.T) {
	eng := sandboxEngine(t)

	_, out, err := describePackage(eng, testCfg())(context.Background(), nil, DescribePackageInput{
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
	if !slices.Contains(desc.Files, "shapes.go") || !slices.Contains(desc.Files, "groups.go") {
		t.Errorf("describe_package(shapes).Files missing entries: %v", desc.Files)
	}

	_, fout, err := describeFile(eng, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "shapes.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file: %v", err)
	}
	file := fout.Results[0]
	if file.Doc == nil || *file.Doc != "Package shapes provides fixture shape types for tests." {
		t.Errorf("describe_file(shapes.go).Doc = %v", file.Doc)
	}

	_, created, err := createFile(eng, testCfg())(context.Background(), nil, CreateFileInput{
		Creates: []CreateFileEntry{{PkgPath: "shapes", FileName: "new_doc.go", Doc: new("New file doc.")}},
	})
	if err != nil {
		t.Fatalf("create_file: %v", err)
	}
	if created.IntroducedDiagnostics != nil {
		t.Errorf("create_file with doc produced diagnostics: %+v", created)
	}
	_, fout2, err := describeFile(eng, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "new_doc.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file(new_doc.go): %v", err)
	}
	newFile := fout2.Results[0]
	if newFile.Doc == nil || *newFile.Doc != "New file doc." {
		t.Errorf("new_doc.go doc = %v, want %q", newFile.Doc, "New file doc.")
	}

	if _, _, err := editFile(eng, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "new_doc.go", Doc: new("")}},
	}); err != nil {
		t.Fatalf("edit_file (clear): %v", err)
	}
	_, fout3, err := describeFile(eng, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{{PkgPath: "shapes", FileName: "new_doc.go"}},
	})
	if err != nil {
		t.Fatalf("describe_file(new_doc.go) after clear: %v", err)
	}
	cleared := fout3.Results[0]
	if cleared.Doc != nil {
		t.Errorf("cleared doc = %v, want nil", cleared.Doc)
	}

	if _, _, err := editFile(eng, testCfg())(context.Background(), nil, EditFileInput{
		Edits: []EditFileEntry{{PkgPath: "shapes", FileName: "nope.go", Doc: new("x")}},
	}); err == nil {
		t.Error("edit_file on a missing file must error")
	}
}

func TestMoveSymbolInputWiring(t *testing.T) {
	eng := sandboxEngine(t)

	if _, _, err := moveSymbol(eng, testCfg())(context.Background(), nil, MoveSymbolInput{
		PkgPath: "shapes", SymbolKey: "NotShape", NewSymbolKey: new("AlsoNotShape"),
	}); err != nil {
		t.Fatalf("move_symbol rename: %v", err)
	}

	if _, _, err := moveSymbol(eng, testCfg())(context.Background(), nil, MoveSymbolInput{
		PkgPath: "shapes", SymbolKey: "Circle.Area", NewSymbolKey: new("Square.Extent"),
	}); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Errorf("mismatched receiver via move_symbol must be refused, got %v", err)
	}

	if _, _, err := moveSymbol(eng, testCfg())(context.Background(), nil, MoveSymbolInput{
		PkgPath: "shapes", SymbolKey: "Circle.Area", NewSymbolKey: new("Circle.Extent"),
	}); err != nil {
		t.Fatalf("move_symbol qualified method rename: %v", err)
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

func TestEditSymbolMultiEntry(t *testing.T) {
	eng := sandboxEngine(t)
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

func TestEditSymbolBatchRefusesDuplicateTarget(t *testing.T) {
	eng := sandboxEngine(t)
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

func TestEditSymbolBatchAbortsWhollyOnFailure(t *testing.T) {
	eng := sandboxEngine(t)
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

func TestEditSymbolBatchRefusesEmpty(t *testing.T) {
	eng := sandboxEngine(t)
	if _, _, err := editSymbol(eng, testCfg())(context.Background(), nil, EditSymbolInput{}); err == nil {
		t.Error("an empty batch must be refused")
	}
}

func TestDeleteTools(t *testing.T) {
	eng := sandboxEngine(t)

	_, out, err := deleteSymbol(eng, testCfg())(context.Background(), nil, DeleteSymbolInput{
		Deletes: []DeleteSymbolEntry{{PkgPath: "shapes", SymbolKey: "Circle"}},
	})
	if err != nil {
		t.Fatalf("delete_symbol: %v", err)
	}
	if !slices.Contains(out.Files["example.com/sandbox/shapes"], "shapes.go") {
		t.Errorf("delete echo missing the touched file: %+v", out)
	}

	_, noop, err := deleteSymbol(eng, testCfg())(context.Background(), nil, DeleteSymbolInput{
		Deletes: []DeleteSymbolEntry{{PkgPath: "shapes", SymbolKey: "Circle"}},
	})
	if err != nil {
		t.Fatalf("delete_symbol (already gone): %v", err)
	}
	if len(noop.Files) != 0 {
		t.Errorf("deleting an already-gone symbol must be a noop, got %+v", noop)
	}

	_, fileNoop, err := deleteFile(eng, testCfg())(context.Background(), nil, DeleteFileInput{
		Deletes: []DeleteFileEntry{{PkgPath: "shapes", FileName: "nosuch.go"}},
	})
	if err != nil {
		t.Fatalf("delete_file (absent): %v", err)
	}
	if len(fileNoop.Files) != 0 {
		t.Errorf("deleting a nonexistent file must be a noop, got %+v", fileNoop)
	}

	_, pkgNoop, err := deletePackage(eng, testCfg())(context.Background(), nil, DeletePackageInput{
		Deletes: []DeletePackageEntry{{PkgPath: "nosuchpkg"}},
	})
	if err != nil {
		t.Fatalf("delete_package (absent): %v", err)
	}
	if len(pkgNoop.Files) != 0 {
		t.Errorf("deleting a nonexistent package must be a noop, got %+v", pkgNoop)
	}
}

func TestDeleteSymbolBatchDuplicateIsHarmless(t *testing.T) {
	// KindSquare's delete already collapses the whole iota group, taking
	// KindCircle with it; a later entry naming KindCircle must not abort
	// the batch just because the first entry already satisfied it.
	eng := sandboxEngine(t)
	_, out, err := deleteSymbol(eng, testCfg())(context.Background(), nil, DeleteSymbolInput{
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

func TestDeleteFileBatchAbortsWhollyOnFailure(t *testing.T) {
	eng := sandboxEngine(t)
	_, _, err := deleteFile(eng, testCfg())(context.Background(), nil, DeleteFileInput{
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
	_, out, err := listFiles(eng, testCfg())(context.Background(), nil, ListFilesInput{PkgPath: "shapes"})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if !slices.Contains(out.Files, "shapes.go") {
		t.Errorf("Error must mean untouched: shapes.go was deleted despite the batch failing, got %v", out.Files)
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

func TestEditFileBatch(t *testing.T) {
	eng := sandboxEngine(t)
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
	eng := sandboxEngine(t)
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

// testCfg returns a toolConfig for tests that call a handler factory
// directly (bypassing Register) and need one to satisfy the signature.
func testCfg() *toolConfig {
	return newToolConfig(20)
}

func TestDescribeSymbolBatch(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "Circle"},
			{PkgPath: "shapes", SymbolKey: "Square"},
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

	if _, _, err := describeSymbol(eng, testCfg())(context.Background(), nil, DescribeSymbolInput{
		Describes: []DescribeSymbolEntry{
			{PkgPath: "shapes", SymbolKey: "Circle"},
			{PkgPath: "shapes", SymbolKey: "Nope"},
		},
	}); err == nil || !strings.Contains(err.Error(), "describes[1]") {
		t.Errorf("expected describes[1] to fail on the missing symbol, got %v", err)
	}
}

func TestDescribeFileBatch(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := describeFile(eng, testCfg())(context.Background(), nil, DescribeFileInput{
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

	if _, _, err := describeFile(eng, testCfg())(context.Background(), nil, DescribeFileInput{
		Describes: []DescribeFileEntry{
			{PkgPath: "shapes", FileName: "shapes.go"},
			{PkgPath: "shapes", FileName: "nope.go"},
		},
	}); err == nil || !strings.Contains(err.Error(), "describes[1]") {
		t.Errorf("expected describes[1] to fail on the missing file, got %v", err)
	}
}

func TestDescribePackageBatch(t *testing.T) {
	eng := sandboxEngine(t)
	_, out, err := describePackage(eng, testCfg())(context.Background(), nil, DescribePackageInput{
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
	if !slices.Contains(out.Results[0].Files, "shapes.go") {
		t.Errorf("Results[0].Files missing shapes.go: %v", out.Results[0].Files)
	}

	if _, _, err := describePackage(eng, testCfg())(context.Background(), nil, DescribePackageInput{
		Describes: []DescribePackageEntry{
			{PkgPath: "shapes"},
			{PkgPath: "nope"},
		},
	}); err == nil || !strings.Contains(err.Error(), "describes[1]") {
		t.Errorf("expected describes[1] to fail on the missing package, got %v", err)
	}
}
