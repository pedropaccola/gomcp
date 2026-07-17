// Package engine is the model's gate: Bootstrap loads the workspace, View
// and Tx expose reads and writes to it, and dto.go translates workspace's
// internal vocabulary into engine's own public types at the boundary —
// nothing from workspace crosses out unaliased.
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
	"github.com/pedropaccola/gomcp/internal/engine/workspace"
	"golang.org/x/tools/go/packages"
)

// Engine owns the gates and the disk boundary: locking, the load
// pipeline, goimports, and flushing live here, while the model itself —
// units, tombstones, position tables, the dependency cache — lives behind
// the workspace.Workspace and is only reshaped through its primitives.
//
// Never call a gate-safe accessor (ModulePath, IsExternal, ...) from
// inside a Read/Edit closure: sync.RWMutex isn't reentrant, so the
// accessor's own lock acquisition deadlocks the calling goroutine against
// itself — no error, no panic, just a permanent hang, and diagnostics()
// won't catch it either, since it's a runtime property, not a type error.
// Resolve any such value before calling Read/Edit and pass it into the
// closure instead.
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
		ws:      workspace.NewWorkspace(),
		logf:    logf,
	}
}

// absPath maps a workspace-relative path back to the filesystem.
func (e *Engine) absPath(p address.RelativePath) string {
	return filepath.Join(e.RootDir, string(p))
}

// Bootstrap loads the workspace from scratch and atomically swaps it in,
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
	e.ws.Reset(module, fset, units)
	return nil
}

// ModulePath returns the workspace's module path under the read lock: the
// gate-safe accessor for callers outside Read and Edit.
func (e *Engine) ModulePath() address.PkgPath {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ws.Module()
}

// load runs the full pipeline — go/packages load, variant selection, package
// building — against disk plus an optional overlay of in-memory contents.
// It is the shared machinery of Bootstrap and the post-mutation recheck.
func (e *Engine) load(ctx context.Context, overlay map[string][]byte) (*token.FileSet, address.PkgPath, map[address.PkgPath]*workspace.Unit, error) {
	fset := token.NewFileSet()
	loadStart := time.Now()
	srcPkgs, err := packages.Load(&packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule,
		Context: ctx,
		Logf:    e.logf,
		Dir:     e.RootDir,
		Fset:    fset,
		Tests:   true,
		Overlay: overlay,
	}, "./...")
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
	units := make(map[address.PkgPath]*workspace.Unit)
	for _, cand := range selected {
		if ctx.Err() != nil {
			return nil, "", nil, fmt.Errorf("workspace load aborted by context cancellation: %w", ctx.Err())
		}
		unit := &workspace.Unit{}
		if cand.prod != nil {
			if unit.Prod, err = e.buildPackage(cand.prod, fset, overlay); err != nil {
				return nil, "", nil, err
			}
		}
		if cand.xtest != nil {
			if unit.XTest, err = e.buildPackage(cand.xtest, fset, overlay); err != nil {
				return nil, "", nil, err
			}
		}
		switch {
		case unit.Prod != nil:
			units[unit.Prod.PkgPath] = unit
		case unit.XTest != nil:
			units[address.PkgPath(strings.TrimSuffix(string(unit.XTest.PkgPath), "_test"))] = unit
		}
	}
	if e.logf != nil {
		e.logf("load: select+build took %v for %d units", time.Since(buildStart), len(units))
	}
	return fset, module, units, nil
}

// buildPackage turns one selected load variant into the engine's Package:
// canonical bytes from overlay-or-disk, the loader's ASTs and type info,
// and a fresh symbol index. Files outside the workspace (generated cgo
// output) are skipped with a diagnostic rather than tracked as
// untouchable paths.
func (e *Engine) buildPackage(srcPkg *packages.Package, fset *token.FileSet, overlay map[string][]byte) (*workspace.Package, error) {
	relPath, err := e.relativePath(srcPkg.Dir)
	if err != nil {
		return nil, fmt.Errorf("package mapping failure for %s: %w", srcPkg.Dir, err)
	}
	pkg := workspace.NewPackage(srcPkg.Name, relPath, address.PkgPath(srcPkg.PkgPath), srcPkg.Types, srcPkg.TypesInfo, false)

	for _, astFile := range srcPkg.Syntax {
		absFilePath := fset.File(astFile.FileStart).Name()
		relFilePath, err := e.relativePath(absFilePath)
		if err != nil || relFilePath.EscapesRoot() {
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
				return nil, fmt.Errorf("failed to read source of %s: %w", relFilePath, err)
			}
		}
		pkg.AddLoadedFile(relFilePath, src, astFile)
	}
	pkg.RebuildIndex()
	e.ingestErrors(pkg, srcPkg.Errors)
	return pkg, nil
}

// ingestErrors converts load errors into Diagnostics, attaching them to the
// file they point at when it is tracked, and to the package otherwise.
func (e *Engine) ingestErrors(pkg *workspace.Package, errs []packages.Error) {
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
			if relFile, err := e.relativePath(absFile); err == nil && !relFile.EscapesRoot() {
				diag.File, diag.Line, diag.Col = relFile, line, col
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
func (e *Engine) relativePath(fullPath string) (address.RelativePath, error) {
	relPath, err := filepath.Rel(e.RootDir, fullPath)
	if err != nil {
		return "", err
	}
	return address.RelativePath(relPath), nil
}

// pkgAt wraps a workspace directory into its canonical package address.
func (e *Engine) pkgAt(dir address.RelativePath) address.PkgPath {
	if dir == "." {
		return e.ws.Module()
	}
	return address.PkgPath(string(e.ws.Module()) + "/" + string(dir))
}

// dirOf unwraps a workspace package address to its directory, comma-ok
// false outside the module: dependencies have no workspace location.
func (e *Engine) dirOf(pkg address.PkgPath) (address.RelativePath, bool) {
	if pkg == e.ws.Module() {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(string(pkg), string(e.ws.Module())+"/"); ok {
		return address.RelativePath(rest), true
	}
	return "", false
}

// fsetOf is the FileSet a package's positions live in: the external
// cache's for dependencies, the workspace FileSet otherwise.
func (e *Engine) fsetOf(pkg *workspace.Package) *token.FileSet {
	return e.ws.FsetOf(pkg)
}

// LoadExternal resolves a dependency by import path into the read-only
// external cache — the lazy counterpart of the workspace load, serving
// exported API only. It is never called under the read gate: callers load
// first, then Read; ExternalPackage resolves what this installed.
//
// The slow part — go/packages.Load plus type-checking — runs with no lock
// held, so it never blocks a concurrent Read or Edit; the engine lock is
// only taken briefly, to check the cache first and to install the result
// after. If Bootstrap or Reload resets the dependency cache while a load
// is in flight, the result is discarded and the load retried against the
// fresh cache instead of installing positions keyed to a FileSet that's no
// longer current.
func (e *Engine) LoadExternal(ctx context.Context, pkg address.PkgPath) error {
	for {
		e.mu.RLock()
		if _, ok := e.ws.ExternalPackage(pkg); ok {
			e.mu.RUnlock()
			return nil
		}
		if err, ok := e.ws.ExternalFailure(pkg); ok {
			e.mu.RUnlock()
			return err
		}
		fset := e.ws.ExternalFset()
		e.mu.RUnlock()

		built, loadErr := e.fetchExternal(ctx, pkg, fset)

		e.mu.Lock()
		if _, ok := e.ws.ExternalPackage(pkg); ok {
			e.mu.Unlock()
			return nil // installed by a concurrent LoadExternal while we worked
		}
		if e.ws.ExternalFset() != fset {
			// Bootstrap/Reload reset the cache mid-load: built's positions (if
			// any) are keyed to a FileSet that's no longer current. Retry
			// against the fresh one rather than install stale positions.
			e.mu.Unlock()
			continue
		}
		if loadErr != nil {
			e.ws.FailExternal(pkg, loadErr)
			e.mu.Unlock()
			return loadErr
		}
		e.ws.InstallExternal(pkg, built)
		e.mu.Unlock()
		return nil
	}
}

// buildExternal is buildPackage's read-only sibling: module-cache files
// are addressed by import-path-qualified pseudo-paths (never flushable),
// and only exported symbols survive indexing — a dependency is API
// surface, not editable code.
func (e *Engine) buildExternal(srcPkg *packages.Package) (*workspace.Package, error) {
	pkg := workspace.NewPackage(srcPkg.Name, "", address.PkgPath(srcPkg.PkgPath), srcPkg.Types, nil, true)
	for _, astFile := range srcPkg.Syntax {
		abs := e.ws.ExternalFset().File(astFile.FileStart).Name()
		src, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("failed to read dependency source %s: %w", abs, err)
		}
		path := address.RelativePath(srcPkg.PkgPath).Join(filepath.Base(abs))
		pkg.AddLoadedFile(path, src, astFile)
	}
	pkg.RebuildIndex()
	return pkg, nil
}

// IsExternal reports whether pkg is resident in the dependency cache — the
// gate-safe accessor behind refusing mutations on read-only packages.
func (e *Engine) IsExternal(pkg address.PkgPath) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.ws.ExternalPackage(pkg)
	return ok
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
		return e.buildExternal(srcPkg)
	}
	for _, srcPkg := range srcPkgs {
		if len(srcPkg.Errors) > 0 {
			return nil, fmt.Errorf("dependency %q failed to load: %s", pkg, srcPkg.Errors[0].Msg)
		}
	}
	return nil, fmt.Errorf("no package at import path %q", pkg)
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
