package workspace

import (
	"go/build/constraint"
	"slices"
	"strings"
	"testing"
)

func TestFileDirectivesDetectsLeadingBlock(t *testing.T) {
	w := simpleFixture(t, "//go:build linux\n//go:generate mockgen -source=foo.go\n\n// Package pkg does something.\npackage pkg\n\nfunc Foo() {}\n")
	file, _, ok := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	if !ok {
		t.Fatal("fixture file not found")
	}
	want := []string{"go:build linux", "go:generate mockgen -source=foo.go"}
	if !slices.Equal(file.Directives, want) {
		t.Errorf("Directives = %v, want %v", file.Directives, want)
	}
	if file.Doc() != "Package pkg does something." {
		t.Errorf("Doc() = %q, directive detection must not disturb it", file.Doc())
	}
}

func TestFileDirectivesNoneWhenAbsent(t *testing.T) {
	w := simpleFixture(t, "// Package pkg does something.\npackage pkg\n\nfunc Foo() {}\n")
	file, _, ok := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	if !ok {
		t.Fatal("fixture file not found")
	}
	if len(file.Directives) != 0 {
		t.Errorf("Directives = %v, want none", file.Directives)
	}
}

func TestSymbolDirectivesFromContiguousDoc(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\n// Foo does something clever.\n//go:noinline\nfunc Foo() {}\n")
	sym, _, ok := w.ResolveSymbol("test.mod/pkg", "Foo")
	if !ok {
		t.Fatal("Foo not found")
	}
	want := []string{"go:noinline"}
	if !slices.Equal(sym.Directives, want) {
		t.Errorf("Directives = %v, want %v", sym.Directives, want)
	}
	if !strings.Contains(sym.Doc(), "Foo does something clever") {
		t.Errorf("Doc() = %q, directive detection must not disturb it", sym.Doc())
	}
}

func TestRenderDirectives(t *testing.T) {
	if got := RenderDirectives(nil); got != nil {
		t.Errorf("RenderDirectives(nil) = %q, want nil", got)
	}
	got := RenderDirectives([]string{"go:build linux", "go:generate mockgen -source=foo.go"})
	want := "//go:build linux\n//go:generate mockgen -source=foo.go\n\n"
	if string(got) != want {
		t.Errorf("RenderDirectives = %q, want %q", got, want)
	}
}

// TestRenderDirectivesGoBuildRecognized confirms the rendered bytes aren't
// merely shaped like a directive but are actually recognized by the Go
// toolchain's own //go:build parser — the real bar RenderDirectives'
// no-space, blank-line-isolated grammar exists to clear.
func TestRenderDirectivesGoBuildRecognized(t *testing.T) {
	w := simpleFixture(t, "package pkg\n")
	if err := w.SwapFile("test.mod/pkg", KindProd, false, "test.mod/pkg/pkg.go", append(RenderDirectives([]string{"go:build linux"}), []byte("package pkg\n")...)); err != nil {
		t.Fatalf("SwapFile: %v", err)
	}
	file, _, ok := w.resolveFile("test.mod/pkg", "test.mod/pkg/pkg.go")
	if !ok {
		t.Fatal("fixture file not found")
	}
	var line string
	for _, raw := range strings.Split(string(file.Src()), "\n") {
		if constraint.IsGoBuild(raw) {
			line = raw
			break
		}
	}
	if line == "" {
		t.Fatalf("go/build/constraint found no //go:build line in:\n%s", file.Src())
	}
	expr, err := constraint.Parse(line)
	if err != nil {
		t.Fatalf("constraint.Parse(%q): %v", line, err)
	}
	if expr.String() != "linux" {
		t.Errorf("parsed constraint = %q, want %q", expr.String(), "linux")
	}
}
