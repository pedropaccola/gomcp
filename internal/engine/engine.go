// Package engine is the Repository: Bootstrap/Reload/Flush own the go/packages.Load pipeline and the disk boundary, and Engine.Read/Edit are the composition root, constructing internal/gate's View and Tx and scoping them to the concurrency contract (Engine.mu, a plain sync.RWMutex). The model itself — units, tombstones, position tables, the dependency cache — lives behind workspace.Workspace; reads and writes against it flow through gate, never through this package's own types.
package engine

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/gate"
	"github.com/pedropaccola/gomcp/internal/workspace"
	"golang.org/x/tools/go/packages"
)

// Engine owns the gates and the disk boundary: locking, the load
// pipeline, goimports, and flushing live here, while the model itself —
// units, tombstones, position tables, the dependency cache — lives behind
// the workspace.Workspace and is only reshaped through its primitives.
//
// ws is guarded by mu, a plain sync.RWMutex: Read takes the read lock for
// its whole call, so any number of Reads run concurrently with each other
// but wait out an in-flight writer; Edit, Flush, Bootstrap, Reload, and
// LoadExternal's install step take the write lock and serialize as the
// sole writer — each builds its change against a private copy and
// publishes it with one assignment, never mutating the published
// Workspace in place.
type Engine struct {
	mu      sync.RWMutex
	RootDir string
	ws      *workspace.Workspace
	logf    func(string, ...any)
}

// NewEngine creates an engine rooted at rootDir. logf enables go/packages
// loader debug output; nil means silent.
func NewEngine(rootDir string, logf func(string, ...any)) *Engine {
	return &Engine{
		RootDir: rootDir,
		logf:    logf,
		ws:      workspace.NewWorkspace(),
	}
}

// absPath maps a file's canonical address back to the filesystem.
func (e *Engine) absPath(p address.FilePath) string {
	return filepath.Join(e.RootDir, p.RelativePath(e.ws.Module()))
}

// Bootstrap loads the workspace from scratch and installs it wholesale,
// discarding any in-memory edits, tombstones, and cached dependencies.
// Broken code is a valid state: per-package load errors become
// Diagnostics, never a bootstrap failure. Only a driver-level failure
// returns an error, leaving any previous state untouched.
func (e *Engine) Bootstrap(ctx context.Context) error {
	fset, module, units, err := e.load(ctx, nil)
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

// ModulePath returns the workspace's module path. Takes a brief read
// lock — safe to call from outside any Read or Edit call, but never from
// inside one: nesting RLock under Edit's write Lock (or under Read's own
// RLock, if a writer is queued in between) would deadlock. No current
// caller does this — each calls ModulePath after its Read/Edit/Flush/
// Reload call has already returned.
func (e *Engine) ModulePath() address.PkgPath {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ws.Module()
}

// load runs the full pipeline against disk plus an optional overlay of
// in-memory contents, for the whole module — the shared entry point for
// Bootstrap and Reload.
func (e *Engine) load(ctx context.Context, overlay map[string][]byte) (*token.FileSet, address.PkgPath, map[address.PkgPath]*workspace.Unit, error) {
	return e.loadInto(ctx, token.NewFileSet(), overlay, "./...")
}

// buildPackage turns one selected load variant into the engine's Package:
// canonical bytes from overlay-or-disk, the loader's ASTs and type info,
// and a fresh symbol index. Files outside the workspace (generated cgo
// output) are skipped with a diagnostic rather than tracked as
// untouchable paths. canonicalPkg addresses every file this builds —
// srcPkg.PkgPath itself only for Package.PkgPath, since the XTest
// variant's own PkgPath differs from the shared unit key (see loadInto).
func (e *Engine) buildPackage(srcPkg *packages.Package, canonicalPkg address.PkgPath, fset *token.FileSet, overlay map[string][]byte) (*workspace.Package, error) {
	relPath, err := e.relativePath(srcPkg.Dir)
	if err != nil {
		return nil, fmt.Errorf("package mapping failure for %s: %w", srcPkg.Dir, err)
	}
	pkg := workspace.NewPackage(srcPkg.Name, relPath, address.PkgPath(srcPkg.PkgPath), srcPkg.Types, srcPkg.TypesInfo, false)

	for _, astFile := range srcPkg.Syntax {
		absFilePath := fset.File(astFile.FileStart).Name()
		relFilePath, err := e.relativePath(absFilePath)
		if err != nil || address.IsOutsideRoot(relFilePath) {
			// Generated files (e.g. cgo output) live outside the workspace;
			// record and move on rather than tracking untouchable paths.
			pkg.Diags = append(pkg.Diags, workspace.Diagnostic{
				Kind: workspace.DiagList,
				Msg:  fmt.Sprintf("skipped file outside workspace: %s", absFilePath),
			})
			continue
		}
		src, ok := overlay[absFilePath]
		if !ok {
			var err error
			if src, err = os.ReadFile(absFilePath); err != nil {
				return nil, fmt.Errorf("failed to read source of %s: %w", absFilePath, err)
			}
		}
		filePath := canonicalPkg.File(filepath.Base(absFilePath))
		pkg.LoadFile(filePath, src, astFile)
	}
	pkg.RebuildIndex()
	e.ingestErrors(pkg, canonicalPkg, srcPkg.Errors)
	return pkg, nil
}

// ingestErrors converts load errors into Diagnostics, attaching them to the
// file they point at when it is tracked, and to the package otherwise.
// canonicalPkg, not pkg.PkgPath, addresses each attributed file — see
// buildPackage.
func (e *Engine) ingestErrors(pkg *workspace.Package, canonicalPkg address.PkgPath, errs []packages.Error) {
	for _, pkgErr := range errs {
		// go list relays compiler output prefixed with "# pkg" and positions
		// pointing into overlay temp copies; the same problems arrive again
		// as parse/type errors with workspace positions, so the relay only
		// adds noise.
		if pkgErr.Kind == packages.ListError && strings.HasPrefix(pkgErr.Msg, "# ") {
			continue
		}
		diag := workspace.Diagnostic{Kind: toDiagKind(pkgErr.Kind), Msg: pkgErr.Msg}
		if absFile, line, col, ok := splitPos(pkgErr.Pos); ok {
			if relFile, err := e.relativePath(absFile); err == nil && !address.IsOutsideRoot(relFile) {
				diag.File, diag.Line, diag.Col = canonicalPkg.File(filepath.Base(relFile)), line, col
			}
		}
		if file, ok := pkg.File(diag.File); ok && diag.File != "" {
			file.Diags = append(file.Diags, diag)
		} else {
			pkg.Diags = append(pkg.Diags, diag)
		}
	}
}

// Returns a path relative to [Engine]'s RootDir
func (e *Engine) relativePath(fullPath string) (string, error) {
	return filepath.Rel(e.RootDir, fullPath)
}

// LoadExternal resolves a dependency by import path into the read-only
// external cache — the lazy counterpart of the workspace load, serving
// exported API only. It is never called under the read gate: callers load
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
func (e *Engine) LoadExternal(ctx context.Context, pkg address.PkgPath) error {
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

		built, loadErr := e.fetchExternal(ctx, pkg, fset)

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

// buildExternal is buildPackage's read-only sibling: module-cache files
// are addressed by import-path-qualified pseudo-paths (never flushable),
// and only exported symbols survive indexing — a dependency is API
// surface, not editable code. fset is the same snapshot fetchExternal
// loaded srcPkg's positions into — never re-derived from e.ws, since this
// runs with no lock held and the published cache can move on beneath it.
func (e *Engine) buildExternal(srcPkg *packages.Package, fset *token.FileSet) (*workspace.Package, error) {
	pkgPath := address.PkgPath(srcPkg.PkgPath)
	pkg := workspace.NewPackage(srcPkg.Name, "", pkgPath, srcPkg.Types, nil, true)
	for _, astFile := range srcPkg.Syntax {
		abs := fset.File(astFile.FileStart).Name()
		src, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("failed to read dependency source %s: %w", abs, err)
		}
		path := pkgPath.File(filepath.Base(abs))
		pkg.LoadFile(path, src, astFile)
	}
	pkg.RebuildIndex()
	return pkg, nil
}

// fetchExternal is LoadExternal's lock-free slow path: the actual
// go/packages.Load and type-check against a specific FileSet snapshot,
// captured by the caller before releasing the engine lock.
func (e *Engine) fetchExternal(ctx context.Context, pkg address.PkgPath, fset *token.FileSet) (*workspace.Package, error) {
	srcPkgs, err := packages.Load(&packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes,
		Context: ctx,
		Logf:    e.logf,
		Dir:     e.RootDir,
		Fset:    fset,
	}, string(pkg))
	if err != nil {
		return nil, fmt.Errorf("dependency %q failed to load: %w", pkg, err)
	}
	for _, srcPkg := range srcPkgs {
		if address.PkgPath(srcPkg.PkgPath) != pkg || srcPkg.Name == "" || len(srcPkg.Syntax) == 0 {
			continue
		}
		return e.buildExternal(srcPkg, fset)
	}
	for _, srcPkg := range srcPkgs {
		if len(srcPkg.Errors) > 0 {
			return nil, fmt.Errorf("dependency %q failed to load: %s", pkg, srcPkg.Errors[0].Msg)
		}
	}
	return nil, fmt.Errorf("no package at import path %q", pkg)
}

// Read runs fn against a consistent snapshot of the workspace, held under
// a read lock for the call's whole duration: any number of Reads run
// concurrently with each other, but a Read waits out an in-flight writer.
// ctx reaches fn's own long-running scans (scanners.go) so a caller can
// cancel or deadline a read; Read itself never blocks on I/O and never
// consults ctx directly.
func (e *Engine) Read(ctx context.Context, fn func(*gate.View) error) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return fn(gate.NewView(e.RootDir, e.ws, ctx))
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
func (e *Engine) Edit(ctx context.Context, fn func(*gate.Tx) error) (*dto.EditReport, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	candidate := e.ws.Clone()

	view := gate.NewView(e.RootDir, candidate, ctx)
	before := view.AllDiagnostics()

	tx := gate.NewTx(view)
	if err := fn(tx); err != nil {
		return nil, err
	}

	stale := func(err error) *dto.EditReport {
		e.ws = candidate
		return &dto.EditReport{Changed: tx.ChangedKeys(), Stale: true, Note: err.Error()}
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
	report := &dto.EditReport{Changed: tx.ChangedKeys()}
	report.Delta, report.Resolved, report.Unrelated = diffDiagnostics(before, view.AllDiagnostics())
	return report, nil
}

// loadInto runs the full pipeline — go/packages load, variant selection,
// package building — against disk plus an optional overlay of in-memory
// contents, restricted to patterns and appending into fset. fset may
// already hold files carried forward by a dirty-scoped recheck (Recheck
// v2, recheckScopedLocked): packages.Load always appends new entries via
// fset.Base(), which is past the end of whatever's already registered, so
// carried-forward files and freshly parsed ones never collide.
func (e *Engine) loadInto(ctx context.Context, fset *token.FileSet, overlay map[string][]byte, patterns ...string) (*token.FileSet, address.PkgPath, map[address.PkgPath]*workspace.Unit, error) {
	loadStart := time.Now()
	srcPkgs, err := packages.Load(&packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule,
		Context: ctx,
		Logf:    e.logf,
		Dir:     e.RootDir,
		Fset:    fset,
		Tests:   true,
		Overlay: overlay,
	}, patterns...)
	if err != nil {
		return nil, "", nil, fmt.Errorf("workspace loading failure: %w", err)
	}
	if e.logf != nil {
		e.logf("load: go/packages took %v for %d package variants (overlay: %d files)",
			time.Since(loadStart), len(srcPkgs), len(overlay))
	}
	buildStart := time.Now()

	var module address.PkgPath
	for _, srcPkg := range srcPkgs {
		if srcPkg.Module != nil && srcPkg.Module.Path != "" {
			module = address.PkgPath(srcPkg.Module.Path)
			break
		}
	}

	// Pass 1: select which load variants to keep, before building anything.
	type candidates struct {
		prod, xtest *packages.Package
	}
	selected := make(map[string]*candidates) // keyed by srcPkg.Dir
	for _, srcPkg := range srcPkgs {
		// Skip the synthesized test binary ("foo.test"): its only syntax is a
		// generated _testmain.go living outside the workspace.
		if strings.HasSuffix(srcPkg.ID, ".test") {
			continue
		}
		cand := selected[srcPkg.Dir]
		if cand == nil {
			cand = &candidates{}
			selected[srcPkg.Dir] = cand
		}
		switch {
		case strings.HasSuffix(srcPkg.Name, "_test"):
			cand.xtest = srcPkg
		// The test-expanded variant "foo [foo.test]" shares PkgPath with the
		// plain "foo" but is a superset of its files: prefer the widest.
		case cand.prod == nil || len(srcPkg.Syntax) > len(cand.prod.Syntax):
			cand.prod = srcPkg
		}
	}

	// Pass 2: build only the winners, keyed by canonical package address —
	// an external-test-only unit answers to its production sibling's path.
	// canonicalPkg (never a variant's own srcPkg.PkgPath, which for the
	// XTest half carries its own distinct "_test"-suffixed identity) is
	// resolved once per candidate and threaded into buildPackage so every
	// file constructed from either half addresses through the same package
	// key Workspace.units is keyed by. Both halves are built before NewUnit
	// assembles them, so a Unit is never observed half-built.
	units := make(map[address.PkgPath]*workspace.Unit)
	for _, cand := range selected {
		if ctx.Err() != nil {
			return nil, "", nil, fmt.Errorf("workspace load aborted by context cancellation: %w", ctx.Err())
		}
		var canonicalPkg address.PkgPath
		switch {
		case cand.prod != nil:
			canonicalPkg = address.PkgPath(cand.prod.PkgPath)
		case cand.xtest != nil:
			canonicalPkg = address.PkgPath(strings.TrimSuffix(cand.xtest.PkgPath, "_test"))
		}
		var prod, xtest *workspace.Package
		if cand.prod != nil {
			if prod, err = e.buildPackage(cand.prod, canonicalPkg, fset, overlay); err != nil {
				return nil, "", nil, err
			}
		}
		if cand.xtest != nil {
			if xtest, err = e.buildPackage(cand.xtest, canonicalPkg, fset, overlay); err != nil {
				return nil, "", nil, err
			}
		}
		units[canonicalPkg] = workspace.NewUnit(prod, xtest)
	}
	if e.logf != nil {
		e.logf("load: select+build took %v for %d units", time.Since(buildStart), len(units))
	}
	return fset, module, units, nil
}

// EnsureFullyChecked forces a full-module recheck and publishes it, but
// only if the current generation was narrowly checked (Recheck v2) —
// otherwise a no-op. The one caller that needs this today is
// search_implementors, ahead of Workspace.SymbolsImplementing: that's the
// one analysis that can't trust a generation mixing packages from two
// different type-checking sessions (see workspace.ErrNarrowlyChecked).
func (e *Engine) EnsureFullyChecked(ctx context.Context) error {
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

// toDiagKind maps the loader's error classification onto ours.
func toDiagKind(kind packages.ErrorKind) workspace.DiagKind {
	switch kind {
	case packages.ListError:
		return workspace.DiagList
	case packages.ParseError:
		return workspace.DiagParse
	case packages.TypeError:
		return workspace.DiagType
	default:
		return workspace.DiagUnknown
	}
}

// splitPos parses the "file:line:col" / "file:line" / "file" position string
// of a [packages.Error]. ok is false when there is no position at all.
func splitPos(pos string) (file string, line, col int, ok bool) {
	if pos == "" || pos == "-" {
		return "", 0, 0, false
	}
	file = pos
	if i := strings.LastIndexByte(file, ':'); i >= 0 {
		if n, err := strconv.Atoi(file[i+1:]); err == nil {
			line, file = n, file[:i]
			if j := strings.LastIndexByte(file, ':'); j >= 0 {
				if m, err := strconv.Atoi(file[j+1:]); err == nil {
					line, col, file = m, n, file[:j]
				}
			}
		}
	}
	return file, line, col, true
}
