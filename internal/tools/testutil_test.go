package tools

import (
	"testing"

	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/storefixture"
)

// testCfg returns a toolConfig for tests that call a handler factory
// directly (bypassing Register) and need one to satisfy the signature.
func testCfg() *toolConfig {
	return newToolConfig(20)
}

func moduleRoot(tb testing.TB) string {
	tb.Helper()
	return storefixture.ModuleRoot(tb)
}

func sandboxStore(tb testing.TB) *store.Store {
	tb.Helper()
	return storefixture.SandboxStore(tb)
}

// findFileEntry finds name's own FileEntry within files, for tests that
// need to inspect more than just presence.
func findFileEntry(files []FileEntry, name string) (FileEntry, bool) {
	for _, f := range files {
		if f.Name == name {
			return f, true
		}
	}
	return FileEntry{}, false
}
