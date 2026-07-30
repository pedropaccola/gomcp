// Package store is the single scoped entry point for the whole model:
// Bootstrap/Reload/Flush sequence the disk boundary under Store's lock,
// delegating the actual go/packages.Load pipeline and raw filesystem
// contact to disk.Loader; Store.Read/Edit construct a View or Tx and
// scope it to the concurrency contract (Store.mu, a plain sync.RWMutex),
// and View/Tx are the query/command surface those calls hand out — View
// exposes narrow, address-keyed read-only queries over one workspace
// snapshot, and Tx (embedding View) adds the mutation verbs that turn the
// Aggregate's decisions into byte-span splices. Both are valid only for
// the single Read or Edit call that constructs and scopes them — never
// held past it. The model itself — units, tombstones, position tables,
// the dependency cache — lives behind workspace.Workspace; reads and
// writes against it flow through View/Tx, never through this package's
// other types. View's read methods live in view.go, with diagnostics
// aggregation split out to view_diagnostics.go; Tx's verbs all live in
// tx.go, with pipeline.go and fragments.go holding the machinery those
// verbs compose on.
package store

import (
	"context"
	"sync"

	"github.com/pedropaccola/gomcp/internal/disk"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// Store owns the query/command boundary and the disk boundary: locking,
// goimports, and dispensing View/Tx live here; the load pipeline and raw
// filesystem contact live behind disk.Loader, called into while Store's
// own lock is held. The model itself — units, tombstones, position
// tables, the dependency cache — lives behind the workspace.Workspace and
// is only reshaped through its primitives.
//
// ws is guarded by mu, a plain sync.RWMutex: Read takes the read lock for
// its whole call, so any number of Reads run concurrently with each other
// but wait out an in-flight writer; Edit, Flush, Bootstrap, Reload, and
// LoadExternal's install step take the write lock and serialize as the
// sole writer — each builds its change against a private copy and
// publishes it with one assignment, never mutating the published
// Workspace in place.
type Store struct {
	mu sync.RWMutex
	disk.Loader
	ws *workspace.Workspace
}

// NewStore creates a store rooted at rootDir. logf enables go/packages
// loader debug output; nil means silent.
func NewStore(rootDir string, logf func(string, ...any)) *Store {
	return &Store{
		Loader: disk.Loader{RootDir: rootDir, Logf: logf},
		ws:     workspace.NewWorkspace(),
	}
}

// Bootstrap loads the workspace from scratch and installs it wholesale,
// discarding any in-memory edits, tombstones, and cached dependencies.
// Broken code is a valid state: per-package load errors become
// Diagnostics, never a bootstrap failure. Only a driver-level failure
// returns an error, leaving any previous state untouched.
func (e *Store) Bootstrap(ctx context.Context) error {
	fset, module, units, err := e.Load(ctx, nil)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ws := workspace.NewWorkspace()
	ws.Reset(module, fset, units)
	e.ws = ws
	return nil
}

// LoadExternal resolves a dependency by import path into the read-only
// external cache — the lazy counterpart of the workspace load, serving
// exported API only. It is never called under a Read call: callers load
// first, then Read; ExternalPackage resolves what this installed.
//
// The slow part — go/packages.Load plus type-checking — runs with no lock
// held, so it never blocks a concurrent Read or Edit; e.mu is only taken
// briefly, to check the cache first and to fork-then-install the result
// after — the fork keeps the mutation off the published Workspace's own
// external map, so a concurrent Read sharing that generation never races
// it. If Bootstrap or Reload resets the dependency cache while a load is
// in flight, the result is discarded and the load retried against the
// fresh cache instead of installing positions keyed to a FileSet that's
// no longer current.
func (e *Store) LoadExternal(ctx context.Context, pkg workspace.PackagePath) error {
	for {
		e.mu.RLock()
		ws := e.ws
		e.mu.RUnlock()
		if _, ok := ws.LookupExternal(pkg); ok {
			return nil
		}
		if err, ok := ws.ExternalFailure(pkg); ok {
			return err
		}
		fset := ws.ExternalFset()

		built, loadErr := e.FetchExternal(ctx, pkg, fset)

		e.mu.Lock()
		cur := e.ws
		if _, ok := cur.LookupExternal(pkg); ok {
			e.mu.Unlock()
			return nil // installed by a concurrent LoadExternal while we worked
		}
		if cur.ExternalFset() != fset {
			// Bootstrap/Reload reset the cache mid-load: built's positions (if
			// any) are keyed to a FileSet that's no longer current. Retry
			// against the fresh one rather than install stale positions.
			e.mu.Unlock()
			continue
		}
		next := cur.ForkExternal()
		if loadErr != nil {
			next.FailExternal(pkg, loadErr)
		} else {
			next.InstallExternal(pkg, built)
		}
		e.ws = next
		e.mu.Unlock()
		return loadErr
	}
}

// Read runs fn against a consistent snapshot of the workspace, held under
// a read lock for the call's whole duration: any number of Reads run
// concurrently with each other, but a Read waits out an in-flight writer.
// ctx reaches fn's own long-running scans (scanners.go) so a caller can
// cancel or deadline a read; Read itself never blocks on I/O and never
// consults ctx directly.
func (e *Store) Read(ctx context.Context, fn func(*View) error) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return fn(NewView(e.ws, ctx))
}

// Edit runs fn against a cloned workspace and commits it with a full
// recheck, mirroring Read. fn returning an error discards every change:
// error means nothing happened. Post-change problems are never errors —
// they arrive as the report's diagnostics delta, because broken code is a
// valid state. If the recheck itself fails, the edit stays applied and the
// report says diagnostics are stale; a valid edit is never rolled back over
// a tooling hiccup. The candidate is built and rechecked entirely off to
// the side, under mu's write lock the whole time, so a concurrent Read
// never observes a half-applied edit.
func (e *Store) Edit(ctx context.Context, fn func(*Tx) error) (*EditReport, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	candidate := e.ws.Clone()

	view := NewView(candidate, ctx)
	before := view.AllDiagnostics()

	tx := NewTx(view)
	if err := fn(tx); err != nil {
		return nil, err
	}

	stale := func(err error) *EditReport {
		e.ws = candidate
		return &EditReport{Changed: tx.ChangedKeys(), Stale: true, Note: err.Error()}
	}
	if err := e.recheckNarrowLocked(ctx, candidate); err != nil {
		return stale(err), nil
	}
	// One bounded self-repair pass for imports goimports cannot see, then
	// re-check to fold the repairs into the echo. Best-effort: it can never
	// fail the already-committed edit.
	if tx.RepairMissingImports() {
		if err := e.recheckNarrowLocked(ctx, candidate); err != nil {
			return stale(err), nil
		}
	}

	e.ws = candidate
	report := &EditReport{Changed: tx.ChangedKeys()}
	report.Delta, report.Resolved, report.Unrelated = diffDiagnostics(before, view.AllDiagnostics())
	return report, nil
}

// EnsureFullyChecked forces a full-module recheck and publishes it, but
// only if the current generation was narrowly checked (Recheck v2) —
// otherwise a no-op. The one caller that needs this today is
// search_implementors, ahead of Workspace.SymbolsImplementing: that's the
// one analysis that can't trust a generation mixing packages from two
// different type-checking sessions (see workspace.ErrNarrowlyChecked).
func (e *Store) EnsureFullyChecked(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.ws.NarrowlyChecked() {
		return nil
	}
	candidate := e.ws.Clone()
	if err := e.recheckFullLocked(ctx, candidate); err != nil {
		return err
	}
	e.ws = candidate
	return nil
}

// EditReport is the echo of a committed transaction: store's own copy,
// relocated next to Store.Edit, its sole constructor.
type EditReport struct {
	Changed   []workspace.FilePath // files created, modified, moved, or deleted by this Tx
	Delta     []Diagnostic         // diagnostics introduced by this transaction
	Resolved  []Diagnostic         // pre-existing diagnostics this transaction fixed
	Unrelated int                  // pre-existing diagnostics this transaction left untouched
	Stale     bool                 // recheck failed: state applied, Delta unavailable
	Note      string               // human-readable recheck failure, when Stale
}
