package engine

import (
	"context"

	"github.com/pedropaccola/gomcp/internal/address"
)

// View is a consistent read-only snapshot of the engine state, valid only
// inside Engine.Read; pointers obtained through it must not escape that
// closure. Its methods live in resolvers.go (X(addr) (..., bool): one
// resource by address, comma-ok, never error), enumerators.go (Xs(scope):
// a scope's resources, always sorted), scanners.go (workspace-wide
// matches; semantic scanners need type information and return an error
// rather than approximate), source.go (exact byte slices of Src, never
// re-printed), and diagnostics.go (problem reports aggregated per scope).
// Scanners compose on enumerators, and both compose on resolvers.
type View struct {
	eng *Engine
}

// Read runs fn against a consistent snapshot of the workspace. Locking lives
// here and nowhere else in the lookup layer.
func (e *Engine) Read(fn func(*View) error) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return fn(&View{eng: e})
}

// Edit runs fn against a cloned workspace and commits it with a full
// recheck, mirroring Read. fn returning an error discards every change:
// error means nothing happened. Post-change problems are never errors —
// they arrive as the report's diagnostics delta, because broken code is a
// valid state. If the recheck itself fails, the edit stays applied and the
// report says diagnostics are stale; a valid edit is never rolled back over
// a tooling hiccup.
func (e *Engine) Edit(ctx context.Context, fn func(*Tx) error) (*EditReport, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	orig := e.ws
	e.ws = orig.Clone()

	view := &View{eng: e}
	before := view.AllDiagnostics()

	tx := &Tx{View: view, changed: make(map[address.RelativePath]bool)}
	if err := fn(tx); err != nil {
		e.ws = orig
		return nil, err
	}

	stale := func(err error) *EditReport {
		return &EditReport{Changed: sortedKeys(tx.changed), Stale: true, Note: err.Error()}
	}
	if err := e.recheckLocked(ctx); err != nil {
		return stale(err), nil
	}
	// One bounded self-repair pass for imports goimports cannot see, then
	// re-check to fold the repairs into the echo. Best-effort: it can never
	// fail the already-committed edit.
	if tx.repairMissingImports() {
		if err := e.recheckLocked(ctx); err != nil {
			return stale(err), nil
		}
	}

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
// whenever preservation cannot be guaranteed); placement.go, fragments.go,
// and extraction.go hold the machinery those verbs compose on. Flush and
// Reload (session.go) are the disk boundary.
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
