// Package disk is the go/packages.Load pipeline and the filesystem's
// other door: Loader owns loading a module (or a dependency) from disk
// plus an in-memory overlay into workspace.Package values, and writing
// workspace bytes back to disk. It holds no lock and no workspace state
// of its own — RootDir and Logf only — so store.Store calls into it while
// store's own lock is held, the same way store calls into workspace's
// primitives; nothing about moving this code here changes when the lock
// is held, only where the code that runs while holding it lives.
package disk

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedropaccola/gomcp/internal/workspace"
	"golang.org/x/tools/go/packages"
)

// Loader owns the module root a Store was constructed with and its
// optional go/packages loader debug logger.
type Loader struct {
	RootDir string
	Logf    func(string, ...any)
}

// Load runs the full pipeline against disk plus an optional overlay of
// in-memory contents, for the whole module.
func (l *Loader) Load(ctx context.Context, overlay map[string][]byte) (*token.FileSet, workspace.PackagePath, map[workspace.PackagePath]*workspace.Unit, error) {
	return l.LoadInto(ctx, token.NewFileSet(), overlay, "./...")
}

// LoadInto runs the full pipeline — go/packages load, variant selection,
// package building — against disk plus an optional overlay of in-memory
// contents, restricted to patterns and appending into fset. fset may
// already hold files carried forward by a dirty-scoped recheck (Recheck
// v2, Store.recheckScopedLocked): packages.Load always appends new
// entries via fset.Base(), which is past the end of whatever's already
// registered, so carried-forward files and freshly parsed ones never
// collide.
func (l *Loader) LoadInto(ctx context.Context, fset *token.FileSet, overlay map[string][]byte, patterns ...string) (*token.FileSet, workspace.PackagePath, map[workspace.PackagePath]*workspace.Unit, error) {
	loadStart := time.Now()
	srcPkgs, err := packages.Load(&packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule,
		Context: ctx,
		Logf:    l.Logf,
		Dir:     l.RootDir,
		Fset:    fset,
		Tests:   true,
		Overlay: overlay,
	}, patterns...)
	if err != nil {
		return nil, "", nil, fmt.Errorf("workspace loading failure: %w", err)
	}
	if l.Logf != nil {
		l.Logf("load: go/packages took %v for %d package variants (overlay: %d files)",
			time.Since(loadStart), len(srcPkgs), len(overlay))
	}
	buildStart := time.Now()

	var module workspace.PackagePath
	for _, srcPkg := range srcPkgs {
		if srcPkg.Module != nil && srcPkg.Module.Path != "" {
			module = workspace.PackagePath(srcPkg.Module.Path)
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
	units := make(map[workspace.PackagePath]*workspace.Unit)
	for _, cand := range selected {
		if ctx.Err() != nil {
			return nil, "", nil, fmt.Errorf("workspace load aborted by context cancellation: %w", ctx.Err())
		}
		var canonicalPkg workspace.PackagePath
		switch {
		case cand.prod != nil:
			canonicalPkg = workspace.PackagePath(cand.prod.PkgPath)
		case cand.xtest != nil:
			canonicalPkg = workspace.PackagePath(strings.TrimSuffix(cand.xtest.PkgPath, "_test"))
		}
		var prod, xtest *workspace.Package
		var err error
		if cand.prod != nil {
			if prod, err = l.buildPackage(cand.prod, canonicalPkg, fset, overlay); err != nil {
				return nil, "", nil, err
			}
		}
		if cand.xtest != nil {
			if xtest, err = l.buildPackage(cand.xtest, canonicalPkg, fset, overlay); err != nil {
				return nil, "", nil, err
			}
		}
		units[canonicalPkg] = workspace.NewUnit(prod, xtest)
	}
	if l.Logf != nil {
		l.Logf("load: select+build took %v for %d units", time.Since(buildStart), len(units))
	}
	return fset, module, units, nil
}

// buildPackage turns one selected load variant into the store's Package:
// canonical bytes from overlay-or-disk, the loader's ASTs and type info,
// and a fresh symbol index. Files outside the workspace (generated cgo
// output) are skipped with a diagnostic rather than tracked as
// untouchable paths. canonicalPkg addresses every file this builds, and
// is Package.ID's own path — srcPkg.PkgPath itself is never used for
// identity, since the XTest variant's own PkgPath differs from the
// shared unit key (see LoadInto). Prod vs XTest is derived from
// srcPkg.Name here, the same rule LoadInto's own Pass 1 already
// classified by — nothing for the caller to separately track and pass in
// sync.
func (l *Loader) buildPackage(srcPkg *packages.Package, canonicalPkg workspace.PackagePath, fset *token.FileSet, overlay map[string][]byte) (*workspace.Package, error) {
	kind := workspace.KindProd
	if strings.HasSuffix(srcPkg.Name, "_test") {
		kind = workspace.KindXTest
	}
	pkg := workspace.NewPackage(srcPkg.Name, canonicalPkg, kind, srcPkg.Types, srcPkg.TypesInfo)

	for _, astFile := range srcPkg.Syntax {
		absFilePath := fset.File(astFile.FileStart).Name()
		relFilePath, err := l.relativePath(absFilePath)
		if err != nil || workspace.IsOutsideRoot(relFilePath) {
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
	l.ingestErrors(pkg, canonicalPkg, srcPkg.Errors)
	return pkg, nil
}

// relativePath returns a path relative to Loader's RootDir.
func (l *Loader) relativePath(fullPath string) (string, error) {
	return filepath.Rel(l.RootDir, fullPath)
}

// ingestErrors converts load errors into Diagnostics, attaching them to the
// file they point at when it is tracked, and to the package otherwise.
// canonicalPkg, not pkg.ID, addresses each attributed file — see
// buildPackage.
func (l *Loader) ingestErrors(pkg *workspace.Package, canonicalPkg workspace.PackagePath, errs []packages.Error) {
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
			if relFile, err := l.relativePath(absFile); err == nil && !workspace.IsOutsideRoot(relFile) {
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

// FetchExternal is LoadExternal's lock-free slow path: the actual
// go/packages.Load and type-check against a specific FileSet snapshot,
// captured by the caller before releasing the store's lock.
func (l *Loader) FetchExternal(ctx context.Context, pkg workspace.PackagePath, fset *token.FileSet) (*workspace.Package, error) {
	srcPkgs, err := packages.Load(&packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes,
		Context: ctx,
		Logf:    l.Logf,
		Dir:     l.RootDir,
		Fset:    fset,
	}, string(pkg))
	if err != nil {
		return nil, fmt.Errorf("dependency %q failed to load: %w", pkg, err)
	}
	for _, srcPkg := range srcPkgs {
		if workspace.PackagePath(srcPkg.PkgPath) != pkg || srcPkg.Name == "" || len(srcPkg.Syntax) == 0 {
			continue
		}
		return l.buildExternal(srcPkg, fset)
	}
	for _, srcPkg := range srcPkgs {
		if len(srcPkg.Errors) > 0 {
			return nil, fmt.Errorf("dependency %q failed to load: %s", pkg, srcPkg.Errors[0].Msg)
		}
	}
	return nil, fmt.Errorf("no package at import path %q", pkg)
}

// buildExternal is buildPackage's read-only sibling: module-cache files
// are addressed by import-path-qualified pseudo-paths (never flushable),
// and only exported symbols survive indexing — a dependency is API
// surface, not editable code. fset is the same snapshot FetchExternal
// loaded srcPkg's positions into — never re-derived from the workspace,
// since this runs with no lock held and the published cache can move on
// beneath it.
func (l *Loader) buildExternal(srcPkg *packages.Package, fset *token.FileSet) (*workspace.Package, error) {
	pkgPath := workspace.PackagePath(srcPkg.PkgPath)
	pkg := workspace.NewPackage(srcPkg.Name, pkgPath, workspace.KindExternal, srcPkg.Types, nil)
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

// AbsPath maps a file's canonical address, relative to module, back to
// the filesystem.
func (l *Loader) AbsPath(module workspace.PackagePath, p workspace.FilePath) string {
	return filepath.Join(l.RootDir, p.RelativePath(module))
}

// WriteFile writes src to abs, an already-resolved absolute path,
// creating any missing parent directories first.
func (l *Loader) WriteFile(abs string, src []byte) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, src, 0o644)
}

// RemoveFile deletes abs, an already-resolved absolute path. Already gone
// is not an error.
func (l *Loader) RemoveFile(abs string) error {
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveEmptyAncestors best-effort removes dir and each now-empty parent
// up to (not including) RootDir, stopping at the first non-empty or
// already-gone directory. A leftover empty directory is disk debris, not
// a modeled entity, so a failure here is silently swallowed rather than
// failing the caller.
func (l *Loader) RemoveEmptyAncestors(dir string) {
	root := filepath.Clean(l.RootDir)
	for dir = filepath.Clean(dir); dir != root && filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			return
		}
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
