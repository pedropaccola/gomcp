// Package enginefixture bootstraps the testdata/sandbox module through a
// real Engine for tests that need it. Split out from internal/testutil
// because Engine imports gate, so a package that reaches Engine can never
// be imported by gate's own (internal) test files without an import cycle
// — engine keeps its own local copy of this logic for exactly that reason;
// this package exists for tools, the one consumer that can safely reach
// through Engine without looping back.
package enginefixture

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedropaccola/gomcp/internal/engine"
)

// ModuleRoot walks up from the current working directory to the go.mod
// that owns it — go test sets the working directory to the calling
// package's own directory, whichever package that is, so this resolves
// correctly regardless of which package's test binary is running.
func ModuleRoot(tb testing.TB) string {
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

// SandboxEngine bootstraps the testdata/sandbox fixture module through a
// real Engine. Mutations stay in memory; a test that Flushes must copy
// the sandbox to a scratch directory first instead of using this fixture
// directly.
func SandboxEngine(tb testing.TB) *engine.Engine {
	tb.Helper()
	e := engine.NewEngine(filepath.Join(ModuleRoot(tb), "testdata", "sandbox"), nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		tb.Fatalf("Bootstrap: %v", err)
	}
	return e
}
