package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

// testCfg returns a toolConfig for tests that call a handler factory
// directly (bypassing Register) and need one to satisfy the signature.
func testCfg() *toolConfig {
	return newToolConfig(20)
}
