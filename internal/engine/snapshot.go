package engine

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
// directly, never back through eng.ws, so a View built mid-transaction
// stays correct even as the candidate is rechecked in place. Its methods
// live in resolvers.go (X(addr) (..., bool): one resource by address,
// comma-ok, never error), enumerators.go (Xs(scope): a scope's resources,
// always sorted), scanners.go (workspace-wide matches; semantic scanners
// need type information and return an error rather than approximate),
// source.go (exact byte slices of Src, never re-printed), and
// diagnostics.go (problem reports aggregated per scope). Scanners compose
// on enumerators, and both compose on resolvers. ctx comes from the Read
// call that created this View; scanners.go's long-running scans check it
// for cancellation, nothing else does.
type View struct {
	eng *Engine
	ws  *workspace.Workspace
	ctx context.Context
}

// Read runs fn against a consistent snapshot of the workspace, loaded
// lock-free: Read never blocks on a writer or on another Read. ctx
// reaches fn's own long-running scans (scanners.go) so a caller can cancel
// or deadline a read; Read itself never blocks on I/O and never consults
// ctx directly.
func (e *Engine) Read(ctx context.Context, fn func(*View) error) error {
	return fn(&View{eng: e, ws: e.ws.Load(), ctx: ctx})
}

// Edit runs fn against a cloned workspace and commits it with a full
// recheck, mirroring Read. fn returning an error discards every change:
// error means nothing happened. Post-change problems are never errors —
// they arrive as the report's diagnostics delta, because broken code is a
// valid state. If the recheck itself fails, the edit stays applied and the
// report says diagnostics are stale; a valid edit is never rolled back over
// a tooling hiccup. The candidate is built and rechecked entirely off to
// the side; e.ws is published with one Store, only once, so a concurrent
// Read never observes a half-applied edit.
func (e *Engine) Edit(ctx context.Context, fn func(*Tx) error) (*EditReport, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	candidate := e.ws.Load().Clone()

	view := &View{eng: e, ws: candidate, ctx: ctx}
	before := view.AllDiagnostics()

	tx := &Tx{View: view, changed: make(map[address.RelativePath]bool)}
	if err := fn(tx); err != nil {
		return nil, err
	}

	stale := func(err error) *EditReport {
		e.ws.Store(candidate)
		return &EditReport{Changed: sortedKeys(tx.changed), Stale: true, Note: err.Error()}
	}
	if err := e.recheckLocked(ctx, candidate); err != nil {
		return stale(err), nil
	}
	// One bounded self-repair pass for imports goimports cannot see, then
	// re-check to fold the repairs into the echo. Best-effort: it can never
	// fail the already-committed edit.
	if tx.repairMissingImports() {
		if err := e.recheckLocked(ctx, candidate); err != nil {
			return stale(err), nil
		}
	}

	e.ws.Store(candidate)
	report := &EditReport{Changed: sortedKeys(tx.changed)}
	report.Delta, report.Resolved, report.Unrelated = diffDiagnostics(before, view.AllDiagnostics())
	return report, nil
}

// Tx is a mutable view over a cloned workspace, embedding View so every
// lookup composes inside a transaction; mid-Tx reads are parse-fresh but
// type-stale until the commit-time recheck (recheck.go). Every content
// mutation is a byte-span splice on a file's canonical Src (pipeline.go)
// — the AST locates spans but is never re-printed, so comments cannot
// drift. Verbs live in creators.go (fail if the address already exists;
// can never destroy), editors.go (fail if the address doesn't exist;
// delete included), and refactorings.go (structure-preserving, refused
// whenever preservation cannot be guaranteed — a verb belongs here only
// if it has exactly one mechanically correct resolution everywhere it
// applies; otherwise it's an Editor, however tempting the automation
// looks); placement.go, fragments.go, and extraction.go hold the
// machinery those verbs compose on. Flush and Reload (session.go) are
// the disk boundary.
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

// EditReport is the echo of a committed transaction.
type EditReport struct {
	Changed   []address.RelativePath // files created, modified, moved, or deleted by this Tx
	Delta     []Diagnostic           // diagnostics introduced by this transaction
	Resolved  []Diagnostic           // pre-existing diagnostics this transaction fixed
	Unrelated int                    // pre-existing diagnostics this transaction left untouched
	Stale     bool                   // recheck failed: state applied, Delta unavailable
	Note      string                 // human-readable recheck failure, when Stale
}
