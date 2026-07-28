package store

import (
	"context"
	"go/types"
	"testing"

	"github.com/pedropaccola/gomcp/internal/testutil"
)

// funcImporter adapts a plain function to types.Importer, so
// sessionTypesFixture can resolve cross-package imports among the
// fixture's own in-memory packages without a real module on disk.
type funcImporter func(path string) (*types.Package, error)

func (f funcImporter) Import(path string) (*types.Package, error) { return f(path) }

// viewFixture builds a single-package Workspace with no type information,
// wrapped in a View — the unit-test fixture for View's read pass-throughs
// and Tx's pipeline mechanics (goimports formatting, SwapFile,
// touch-tracking), with no real go/packages.Load and no real filesystem.
// Delegates to testutil.SimpleFixture.
func viewFixture(tb testing.TB, src string) *View {
	tb.Helper()
	return NewView(testutil.SimpleFixture(tb, src), context.Background())
}

// viewTypesFixture builds a multi-package Workspace with real go/types
// information, wrapped in a View — the fixture for Tx verbs whose
// underlying workspace analysis (MoveSymbol's rename, MoveFile's
// cross-package checks) needs real type identity, not just AST/index
// data. Delegates to testutil.TypesFixture, pre-qualifying each key under
// "test.mod/" — matching how a real workspace address decomposes
// (module + bare directory) rather than testutil's raw-import-path
// convention — so a cross-package reference in fixture source must
// import the full "test.mod/<key>" path to resolve.
func viewTypesFixture(tb testing.TB, srcs map[string]string) *View {
	tb.Helper()
	qualified := make(map[string]string, len(srcs))
	for dir, src := range srcs {
		qualified["test.mod/"+dir] = src
	}
	return NewView(testutil.TypesFixture(tb, qualified), context.Background())
}
