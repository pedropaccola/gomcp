package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
)

// moduleRoot walks up from the package directory to the go.mod.
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

// sandboxEngine bootstraps the fixture module. Mutations stay in memory;
// tests that Flush must use copySandbox instead.
func sandboxEngine(tb testing.TB) *Engine {
	tb.Helper()
	e := NewEngine(filepath.Join(moduleRoot(tb), "testdata", "sandbox"), nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		tb.Fatalf("Bootstrap: %v", err)
	}
	return e
}

// copySandbox clones the fixture module into a scratch dir so Flush tests
// can touch disk without contaminating testdata.
func copySandbox(tb testing.TB) string {
	tb.Helper()
	src := filepath.Join(moduleRoot(tb), "testdata", "sandbox")
	dst := tb.TempDir()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		tb.Fatal(err)
	}
	return dst
}

func matchKeys(matches []Match) []string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Pkg.Path().String()+":"+m.Sym.Key())
	}
	return out
}

// spkg addresses a sandbox package the way the engine now expects:
// module-qualified.
func spkg(dir string) address.PkgPath {
	return address.PkgPath("example.com/sandbox/" + dir)
}
