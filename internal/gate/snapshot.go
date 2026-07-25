package gate

import (
	"context"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// View is a consistent read-only snapshot of the engine state, valid only
// inside Engine.Read or Engine.Edit; pointers obtained through it must not
// escape that closure. ws is the exact snapshot this View was built
// against — the published Workspace during a Read, or the transaction's
// own candidate during an Edit — and every method resolves through it
// directly. Its methods live in resolvers.go (X(addr) (..., bool): one
// resource by address, comma-ok, never error), enumerators.go (Xs(scope):
// a scope's resources, always sorted), scanners.go (workspace-wide
// matches; semantic scanners need type information and return an error
// rather than approximate), source.go (exact byte slices of Src, never
// re-printed), and diagnostics.go (problem reports aggregated per scope).
// Scanners compose on enumerators, and both compose on resolvers. ctx
// comes from the Read call that created this View; scanners.go's
// long-running scans check it for cancellation, nothing else does.
type View struct {
	rootDir string // absolute workspace root, joined against a relative path for goimports
	ws      *workspace.Workspace
	ctx     context.Context
}

// Tx is a mutable view over a cloned workspace, embedding View so every
// lookup composes inside a transaction; mid-Tx reads are parse-fresh but
// type-stale until the commit-time recheck. Every content mutation is a
// byte-span splice on a file's canonical Src (pipeline.go) — the AST
// locates spans but is never re-printed, so comments cannot drift. Verbs
// live in creators.go (fail if the address already exists; can never
// destroy), editors.go (fail if the address doesn't exist; delete
// included), and refactorings.go (structure-preserving, refused whenever
// preservation cannot be guaranteed — a verb belongs here only if it has
// exactly one mechanically correct resolution everywhere it applies;
// otherwise it's an Editor, however tempting the automation looks);
// fragments.go and extraction.go hold the machinery those verbs compose
// on. Flush and Reload, the disk boundary, live on Engine, one layer up.
type Tx struct {
	*View
	changed map[address.RelativePath]bool // paths this transaction touched
}

// touch records paths as changed by this transaction; every verb reports
// its footprint here regardless of prior dirtiness.
func (tx *Tx) touch(paths ...address.RelativePath) {
	for _, path := range paths {
		tx.changed[path] = true
	}
}

// ChangedKeys is the sorted set of paths this transaction touched, for
// Engine.Edit's report.
func (tx *Tx) ChangedKeys() []address.RelativePath {
	return sortedKeys(tx.changed)
}

// NewView constructs a View over ws, rooted at rootDir. Engine's Read and
// Edit are the only callers.
func NewView(rootDir string, ws *workspace.Workspace, ctx context.Context) *View {
	return &View{rootDir: rootDir, ws: ws, ctx: ctx}
}

// NewTx constructs a Tx over view. Engine.Edit is the only caller.
func NewTx(view *View) *Tx {
	return &Tx{View: view, changed: make(map[address.RelativePath]bool)}
}
