package tools

import (
	"context"
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
	Register(server, sandboxEngine(t))
}

// TestToolAnnotations asserts the annotations exactly as a client sees them
// over the wire: the workspace is a closed world, reads are read-only, and
// only Creators are non-destructive among the mutators.
func TestToolAnnotations(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(server, sandboxEngine(t))

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
	_, out, err := listPackages(eng)(context.Background(), nil, ListPackagesInput{})
	if err != nil {
		t.Fatalf("list_packages: %v", err)
	}
	for _, want := range []string{"broken", "shapes", "use"} {
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

	_, files, err := listFiles(eng)(context.Background(), nil, ListFilesInput{Package: "shapes"})
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if !slices.Contains(files.Files, "groups.go") {
		t.Errorf("list_files missing groups.go: %v", files.Files)
	}

	_, syms, err := listSymbols(eng)(context.Background(), nil, ListSymbolsInput{
		Package: "shapes",
		File:    "groups.go",
	})
	if err != nil {
		t.Fatalf("list_symbols: %v", err)
	}
	var kindCircle *SymbolEntry
	for i, s := range syms.Symbols {
		if s.Key == "KindCircle" {
			kindCircle = &syms.Symbols[i]
		}
		if s.Key == "Circle" {
			t.Error("file filter leaked a symbol from shapes.go")
		}
	}
	if kindCircle == nil || kindCircle.Kind != "const" || !strings.Contains(kindCircle.Summary, "Kind = iota") {
		t.Errorf("KindCircle entry wrong: %+v", kindCircle)
	}

	if _, _, err := listSymbols(eng)(context.Background(), nil, ListSymbolsInput{Package: "no/such/pkg"}); err == nil {
		t.Error("list_symbols on a missing package must error")
	}
}

func TestDescribers(t *testing.T) {
	eng := sandboxEngine(t)

	_, typ, err := describeType(eng)(context.Background(), nil, DescribeTypeInput{
		Package: "shapes", Name: "Circle",
	})
	if err != nil {
		t.Fatalf("describe_type: %v", err)
	}
	if !strings.Contains(typ.Source, "type Circle struct") || typ.File != "shapes.go" {
		t.Errorf("describe_type(Circle) wrong: file=%s", typ.File)
	}
	if !slices.ContainsFunc(typ.Methods, func(s string) bool {
		return strings.Contains(s, "Area() float64")
	}) {
		t.Errorf("describe_type(Circle) missing Area: %v", typ.Methods)
	}

	_, fn, err := describeFunction(eng)(context.Background(), nil, DescribeFunctionInput{
		Package: "use", Name: "NewCircle",
	})
	if err != nil {
		t.Fatalf("describe_function: %v", err)
	}
	if !strings.Contains(fn.Source, "func NewCircle(") {
		t.Error("describe_function(NewCircle) missing declaration")
	}

	_, m, err := describeMethod(eng)(context.Background(), nil, DescribeMethodInput{
		Package: "shapes", Type: "Circle", Name: "Area",
	})
	if err != nil {
		t.Fatalf("describe_method: %v", err)
	}
	if !strings.Contains(m.Source, "func (c Circle) Area()") {
		t.Error("describe_method(Circle.Area) missing declaration")
	}

	if _, _, err := describeFunction(eng)(context.Background(), nil, DescribeFunctionInput{
		Package: "shapes", Name: "Circle",
	}); err == nil || !strings.Contains(err.Error(), "describe_") {
		t.Errorf("kind mismatch must error with a describe_* hint, got %v", err)
	}
}

func TestFindersAndDiagnostics(t *testing.T) {
	eng := sandboxEngine(t)

	_, like, err := searchDeclarationsLike(eng)(context.Background(), nil, SearchLikeInput{Name: "area"})
	if err != nil {
		t.Fatalf("search_declarations_like: %v", err)
	}
	if !slices.ContainsFunc(like.Matches, func(m MatchEntry) bool {
		return m.Key == "Circle.Area" && m.Package == "shapes" && m.Kind == "method"
	}) {
		t.Errorf("search_declarations_like(area) missing Circle.Area: %v", like.Matches)
	}

	_, src, err := searchSource(eng)(context.Background(), nil, SearchSourceInput{Regexp: `(?m)^type Embedded struct`})
	if err != nil {
		t.Fatalf("search_source: %v", err)
	}
	if len(src.Matches) != 1 || src.Matches[0].Key != "Embedded" {
		t.Errorf("search_source(type Embedded) = %v, want single Embedded", src.Matches)
	}
	if _, _, err := searchSource(eng)(context.Background(), nil, SearchSourceInput{Regexp: "("}); err == nil {
		t.Error("search_source must reject an invalid regexp")
	}

	_, diags, err := diagnostics(eng)(context.Background(), nil, DiagnosticsInput{})
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if !slices.ContainsFunc(diags.Diagnostics, func(s string) bool { return strings.HasPrefix(s, "[type]") }) {
		t.Errorf("sandbox type error missing from inventory: %v", diags.Diagnostics)
	}
}

func TestSemanticFinders(t *testing.T) {
	eng := sandboxEngine(t)

	_, impl, err := searchImplementors(eng)(context.Background(), nil, SearchImplementorsInput{
		Package: "shapes", Name: "Shape",
	})
	if err != nil {
		t.Fatalf("search_implementors: %v", err)
	}
	if !slices.ContainsFunc(impl.Matches, func(m MatchEntry) bool { return m.Key == "Embedded" }) {
		t.Errorf("search_implementors(Shape) missing promoted-method implementor Embedded: %v", impl.Matches)
	}

	_, refs, err := searchReferences(eng)(context.Background(), nil, SearchReferencesInput{
		Package: "shapes", Key: "Circle",
	})
	if err != nil {
		t.Fatalf("search_references: %v", err)
	}
	if !slices.ContainsFunc(refs.Matches, func(m MatchEntry) bool {
		return m.Package == "use" && m.Key == "NewCircle"
	}) {
		t.Errorf("search_references(Circle) missing use:NewCircle: %v", refs.Matches)
	}

	if _, _, err := searchImplementors(eng)(context.Background(), nil, SearchImplementorsInput{
		Package: "shapes", Name: "Circle",
	}); err == nil || !strings.Contains(err.Error(), "interface") {
		t.Errorf("search_implementors on a struct must error mentioning interface, got %v", err)
	}
}

func TestMutationTools(t *testing.T) {
	eng := sandboxEngine(t)

	_, created, err := createDeclaration(eng)(context.Background(), nil, CreateDeclarationInput{
		Package: "shapes", File: "extra.go",
		Source: "func Twice(x float64) float64 { return 2 * x }",
	})
	if err != nil {
		t.Fatalf("create_declaration: %v", err)
	}
	if !slices.Contains(created.Files["shapes"], "extra.go") || len(created.Diagnostics) != 0 {
		t.Errorf("create echo wrong: %+v", created)
	}

	_, edited, err := editDeclaration(eng)(context.Background(), nil, EditDeclarationInput{
		Package: "shapes", Key: "Circle",
		Source: "type Circle struct{ Radius float64 }",
	})
	if err != nil {
		t.Fatalf("edit_declaration: %v", err)
	}
	if !slices.ContainsFunc(edited.Diagnostics, func(s string) bool {
		return strings.Contains(s, "use/use.go")
	}) {
		t.Errorf("edit echo missing the blast radius in use/use.go: %+v", edited)
	}

	_, healed, err := editDeclaration(eng)(context.Background(), nil, EditDeclarationInput{
		Package: "shapes", Key: "Circle",
		Source: "type Circle struct{ R float64 }",
	})
	if err != nil {
		t.Fatalf("edit_declaration (heal): %v", err)
	}
	if len(healed.Resolved) == 0 || len(healed.Diagnostics) != 0 {
		t.Errorf("healing echo must report resolved and nothing introduced: %+v", healed)
	}

	if _, _, err := editDeclaration(eng)(context.Background(), nil, EditDeclarationInput{
		Package: "shapes", Key: "Nope", Source: "func Nope() {}",
	}); err == nil {
		t.Error("editing a missing declaration must error")
	}
}

func TestAddressForms(t *testing.T) {
	eng := sandboxEngine(t)

	// Package arguments never accept file names, on any tool.
	if _, _, err := listSymbols(eng)(context.Background(), nil, ListSymbolsInput{
		Package: "shapes/shapes.go",
	}); err == nil || !strings.Contains(err.Error(), "names a file") {
		t.Errorf("file-named package must be refused, got %v", err)
	}
	if _, _, err := deletePackage(eng)(context.Background(), nil, DeletePackageInput{
		Package: "shapes/shapes.go",
	}); err == nil || !strings.Contains(err.Error(), "names a file") {
		t.Errorf("file-named package on a destructive tool must be refused, got %v", err)
	}

	// File arguments accept a bare name or a path that agrees with the
	// package; contradictions and non-*.go forms are refused.
	if _, syms, err := listSymbols(eng)(context.Background(), nil, ListSymbolsInput{
		Package: "shapes", File: "shapes/groups.go",
	}); err != nil || len(syms.Symbols) == 0 {
		t.Errorf("file path agreeing with package must be accepted, got %v", err)
	}
	if _, _, err := listSymbols(eng)(context.Background(), nil, ListSymbolsInput{
		Package: "shapes", File: "use/use.go",
	}); err == nil || !strings.Contains(err.Error(), "does not live in") {
		t.Errorf("file outside the package must be refused, got %v", err)
	}
	if _, _, err := createDeclaration(eng)(context.Background(), nil, CreateDeclarationInput{
		Package: "shapes", File: "notgo", Source: "func X() {}",
	}); err == nil || !strings.Contains(err.Error(), "bare *.go name") {
		t.Errorf("non-.go file name must be refused, got %v", err)
	}

	// File-addressed mutations speak (package, file) like everything else.
	if _, _, err := deleteFile(eng)(context.Background(), nil, DeleteFileInput{
		Package: "use", File: "alias.go",
	}); err != nil {
		t.Errorf("delete_file with (package, file): %v", err)
	}
	if _, _, err := renameFile(eng)(context.Background(), nil, RenameFileInput{
		Package: "shapes", File: "shapes/groups.go", NewName: "grouped.go",
	}); err != nil {
		t.Errorf("rename_file with an agreeing file path: %v", err)
	}
}
