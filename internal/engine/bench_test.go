package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The benchmarks split the two cost regimes: reads (in-memory, should be
// microseconds) and the load pipeline (go list subprocess + type-check,
// the only genuinely expensive operations). Run with:
//
//	go test -bench . -benchtime 3x ./internal/engine
//
// The recheck-v2 decision (import-graph invalidation) is gated on
// BenchmarkEditRoundTrip and BenchmarkBootstrapGenerated numbers.

func BenchmarkBootstrapSandbox(b *testing.B) {
	root := filepath.Join(moduleRoot(b), "testdata", "sandbox")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := NewEngine(root, nil).Bootstrap(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEditRoundTrip measures one full mutation transaction: splice,
// goimports, reparse, full-workspace recheck, delta computation.
func BenchmarkEditRoundTrip(b *testing.B) {
	e := sandboxEngine(b)
	bodies := []string{
		"type NotShape struct{}",
		"type NotShape struct{ pad int }",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := e.Edit(context.Background(), func(tx *Tx) error {
			return tx.EditSymbol(spkg("shapes"), "NotShape", bodies[i%2])
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScans(b *testing.B) {
	e := sandboxEngine(b)
	re := regexp.MustCompile(`append\(`)
	b.Run("SymbolsLike", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			e.Read(context.Background(), func(v *View) error {
				if len(v.SymbolsLike("area")) == 0 {
					b.Fatal("no matches")
				}
				return nil
			})
		}
	})
	b.Run("SymbolsRegexp", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			e.Read(context.Background(), func(v *View) error {
				if len(v.SymbolsRegexp(re)) == 0 {
					b.Fatal("no matches")
				}
				return nil
			})
		}
	})
	b.Run("SymbolsReferencing", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			e.Read(context.Background(), func(v *View) error {
				matches, err := v.SymbolsReferencing(spkg("shapes"), "Circle")
				if err != nil || len(matches) == 0 {
					b.Fatalf("references: %v %v", matches, err)
				}
				return nil
			})
		}
	})
}

// BenchmarkBootstrapGenerated exposes scaling on a synthetic workspace: a
// chain of packages, each importing the previous one.
func BenchmarkBootstrapGenerated(b *testing.B) {
	root := genWorkspace(b, 20, 8)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := NewEngine(root, nil).Bootstrap(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// genWorkspace writes a synthetic module: pkgCount chained packages with
// declsPerPkg functions each.
func genWorkspace(tb testing.TB, pkgCount, declsPerPkg int) string {
	tb.Helper()
	root := tb.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/gen\n\ngo 1.21\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < pkgCount; i++ {
		name := fmt.Sprintf("p%02d", i)
		var src strings.Builder
		fmt.Fprintf(&src, "package %s\n\n", name)
		if i > 0 {
			fmt.Fprintf(&src, "import \"example.com/gen/p%02d\"\n\n", i-1)
		}
		fmt.Fprintf(&src, "// T carries a value.\ntype T struct{ V int }\n\n")
		for d := 0; d < declsPerPkg; d++ {
			fmt.Fprintf(&src, "func F%d() int {\n", d)
			if i > 0 {
				fmt.Fprintf(&src, "\treturn p%02d.F%d() + %d\n", i-1, d, d)
			} else {
				fmt.Fprintf(&src, "\treturn %d\n", d)
			}
			fmt.Fprintf(&src, "}\n\n")
		}
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".go"), []byte(src.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}
