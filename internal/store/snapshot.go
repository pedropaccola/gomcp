package store

import (
	"context"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// View is a consistent read-only snapshot of the workspace state, valid
// only inside Store.Read or Store.Edit; pointers obtained through it must
// not escape that closure. ws is the exact snapshot this View was built
// against — the published Workspace during a Read, or the transaction's
// own candidate during an Edit — and every method resolves through it
// directly. Its read methods live in view.go: narrow, address-keyed
// accessors (HasPackage, PackageDoc, PackageFiles, PackageSymbols,
// Symbol, Methods, ResolveType, ResolveFile, FileDoc, DeclSource,
// SpecSource, Signature) each derived from an actual tools call site, and
// the Symbols* scanners, which need type information and return an error
// rather than approximate when the current generation can't answer
// safely. Diagnostics aggregation lives separately in
// view_diagnostics.go. ctx comes from the Read call that created this
// View; the scanners check it for cancellation, nothing else does.
type View struct {
	ws  *workspace.Workspace
	ctx context.Context
}

// Tx is a mutable view over a cloned workspace, embedding View so every
// lookup composes inside a transaction; mid-Tx reads are parse-fresh but
// type-stale until the commit-time recheck. Every content mutation is a
// byte-span splice on a file's canonical Src (pipeline.go) — the AST
// locates spans but is never re-printed, so comments cannot drift. Every
// verb lives in tx.go, grouped by the same three shapes the tests are
// still split by (creators fail if the address already exists and can
// never destroy; editors fail if the address doesn't exist, delete
// included; refactorings are structure-preserving, refused whenever
// preservation cannot be guaranteed — a verb belongs there only if it has
// exactly one mechanically correct resolution everywhere it applies,
// otherwise it's an editor, however tempting the automation looks);
// fragments.go holds the agent-source parsing/classification machinery
// those verbs compose on. Flush and Reload, the disk boundary, live on
// Store, one layer up.
type Tx struct {
	*View
	changed         map[workspace.FilePath]bool // paths this transaction touched
	directiveDeltas []DirectiveDelta            // directive changes recorded by this transaction, in call order
}

// ChangedKeys is the sorted set of paths this transaction touched, for
// Store.Edit's report.
func (tx *Tx) ChangedKeys() []workspace.FilePath {
	return sortedKeys(tx.changed)
}

// NewView constructs a View over ws. Store's Read and Edit are the only
// callers.
func NewView(ws *workspace.Workspace, ctx context.Context) *View {
	return &View{ws: ws, ctx: ctx}
}

// NewTx constructs a Tx over view. Store.Edit is the only caller.
func NewTx(view *View) *Tx {
	return &Tx{View: view, changed: make(map[workspace.FilePath]bool)}
}
