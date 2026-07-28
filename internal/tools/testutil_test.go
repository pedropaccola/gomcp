package tools

import (
	"testing"

	"github.com/pedropaccola/gomcp/internal/engine"
	"github.com/pedropaccola/gomcp/internal/enginefixture"
)

// testCfg returns a toolConfig for tests that call a handler factory
// directly (bypassing Register) and need one to satisfy the signature.
func testCfg() *toolConfig {
	return newToolConfig(20)
}

func moduleRoot(tb testing.TB) string {
	tb.Helper()
	return enginefixture.ModuleRoot(tb)
}

func sandboxEngine(tb testing.TB) *engine.Engine {
	tb.Helper()
	return enginefixture.SandboxEngine(tb)
}
