package workspace

import (
	"go/token"
	"iter"
	"maps"
	"slices"
)

// Workspace is the one mutable root of the model: the prod/xtest package
// maps, tombstone masks, position tables, and the dependency cache live
// behind it, and every structural change flows through its primitives.
// The fields are unexported by construction — the compiler, not
// convention, keeps arbitrary code from reshaping the model.
//
// Clone is lazy copy-on-write, not a deep copy: prod, xtest, packages, and
// the tombstone map start shared with whatever generation this one was
// cloned from, and fork only on first mutation, one map/package at a time
// (ensureProdForked, ensureXTestForked, ensureRemovedForked,
// ensurePackageForked) — a Tx touching 2 packages out of 80 forks exactly
// those 2. prodForked, xtestForked, and removedForked are reset by Clone,
// so each generation forks its own copy exactly once; forkedPkgs tracks
// which *Package objects are already private to this generation
// specifically (reset to nil by Clone too), since a pointer already
// forked in one generation is shared and un-forked again the moment a
// later Clone hands it to the next.
//
// Not safe for concurrent use: every method assumes exclusive access,
// synchronized externally — internal/store.Store's own mutex is the
// only caller that does this today.
type Workspace struct {
	module      PackagePath
	fset        *token.FileSet
	prod        map[PackagePath]*Package
	xtest       map[PackagePath]*Package
	prodForked  bool
	xtestForked bool

	// narrowlyChecked marks a generation assembled by a dirty-scoped
	// recheck (Rebuild's narrow argument): some packages were carried
	// forward unchanged from an earlier type-checking session rather than
	// rebuilt in this one. ObjectKey-based matching tolerates that fine, but
	// SymbolsImplementing's types.Implements cannot — see its own doc
	// comment and ErrNarrowlyChecked.
	narrowlyChecked bool

	// removed maps tombstoned paths (deleted or renamed away in-memory) to
	// the package that owned them and the overlay mask that hides their
	// on-disk content from rechecks; Flush unlinks them. go/packages
	// overlays cannot remove files, only replace their content, hence the
	// mask. The owning package is captured here, at tombstone-creation
	// time, rather than re-derived from the path later: every caller that
	// tombstones a path already has its package in hand.
	removed       map[FilePath]tombstoneEntry
	removedForked bool

	// forkedPkgs marks which *Package objects this generation has already
	// privately forked — see ensurePackageForked.
	forkedPkgs map[*Package]bool

	// The read-only dependency cache. External positions live in their own
	// FileSet — workspace swaps must not invalidate cached positions.
	// Negative results are cached too, so a mistyped address costs one
	// load per session.
	external     map[PackagePath]*Package
	externalErr  map[PackagePath]error
	externalFset *token.FileSet
}

// NewWorkspace creates an empty model, ready for a Reset from the first
// load.
func NewWorkspace() *Workspace {
	return &Workspace{
		fset:         token.NewFileSet(),
		prod:         make(map[PackagePath]*Package),
		xtest:        make(map[PackagePath]*Package),
		removed:      make(map[FilePath]tombstoneEntry),
		external:     make(map[PackagePath]*Package),
		externalErr:  make(map[PackagePath]error),
		externalFset: token.NewFileSet(),
	}
}

// Reset replaces the whole model with a fresh load's truth, discarding
// tombstones and the dependency cache — the bootstrap swap. prod/xtest
// come from the loader and are trusted as its output. Always a full
// load, so narrowlyChecked clears.
func (w *Workspace) Reset(module PackagePath, fset *token.FileSet, prod, xtest map[PackagePath]*Package) {
	w.module = module
	w.fset = fset
	w.prod = prod
	w.xtest = xtest
	w.narrowlyChecked = false
	w.removed = make(map[FilePath]tombstoneEntry)
	w.external = make(map[PackagePath]*Package)
	w.externalErr = make(map[PackagePath]error)
	w.externalFset = token.NewFileSet()
}

// Rebuild replaces the Prod/XTest maps and their position table with a
// recheck's output, keeping tombstones, module identity, and the
// dependency cache — the post-mutation swap. narrow marks whether this
// was a dirty-scoped recheck (some packages carried forward from an
// earlier session) rather than a full one — see narrowlyChecked.
func (w *Workspace) Rebuild(fset *token.FileSet, prod, xtest map[PackagePath]*Package, narrow bool) {
	w.fset = fset
	w.prod = prod
	w.xtest = xtest
	w.narrowlyChecked = narrow
}

// Module is the workspace's module path: the prefix of every workspace
// package address.
func (w *Workspace) Module() PackagePath {
	return w.module
}

// FileSet is the position table of the workspace's own packages; see
// FsetOf for the owner-aware choice.
func (w *Workspace) FileSet() *token.FileSet {
	return w.fset
}

// FsetOf is the FileSet a package's positions live in: the external
// cache's for dependencies, the workspace FileSet otherwise.
func (w *Workspace) FsetOf(pkg *Package) *token.FileSet {
	if pkg != nil && pkg.ID.Kind() == KindExternal {
		return w.externalFset
	}
	return w.fset
}

// MemberKeys enumerates every workspace address with a Prod and/or XTest
// package, sorted — determinism by construction: the raw maps never
// leave the workspace.
func (w *Workspace) MemberKeys() []PackagePath {
	keys := make(map[PackagePath]bool, len(w.prod)+len(w.xtest))
	for pkg := range w.prod {
		keys[pkg] = true
	}
	for pkg := range w.xtest {
		keys[pkg] = true
	}
	return slices.Sorted(maps.Keys(keys))
}

// InstallProd maps a Prod package at an address; creation and package
// moves end here. Exported for tests only (internal/testutil's fixture
// builders construct workspace states no production verb needs to
// reach, like a package with pre-seeded diagnostics) — CreatePackage/
// MovePackage are the real production doors onto this.
func (w *Workspace) InstallProd(pkg PackagePath, p *Package) {
	w.ensureProdForked()
	w.prod[pkg] = p
}

// removeMembers unmaps an address's Prod and XTest packages without
// tombstoning their files — package moves relocate files individually
// first. DeletePackage-style removal tombstones each file, then removes
// the unit.
func (w *Workspace) removeMembers(pkg PackagePath) {
	w.ensureProdForked()
	w.ensureXTestForked()
	delete(w.prod, pkg)
	delete(w.xtest, pkg)
}

// Files yields every file in the workspace, paired with the address and
// kind of the package that owns it — the one primitive under every
// caller that otherwise hand-walks MemberKeys/MembersOf/Files itself.
func (w *Workspace) Files() iter.Seq2[FileRef, *File] {
	return func(yield func(FileRef, *File) bool) {
		for _, addr := range w.MemberKeys() {
			for _, pkg := range w.MembersOf(addr) {
				for _, file := range pkg.Files() {
					if !yield(FileRef{Pkg: addr, Kind: pkg.ID.Kind()}, file) {
						return
					}
				}
			}
		}
	}
}

// InstallXTest maps an XTest package at an address — InstallProd's XTest
// sibling, same rationale.
func (w *Workspace) InstallXTest(pkg PackagePath, p *Package) {
	w.ensureXTestForked()
	w.xtest[pkg] = p
}

// MembersOf returns pkg's non-nil Prod and XTest packages, Prod before
// XTest — 0 to 2 entries: every caller that needs "every half of this
// address" composes on this. This shadowing order (Prod, then XTest) is
// also the default symbol/file resolution order everywhere that
// composes on MembersOf.
func (w *Workspace) MembersOf(pkg PackagePath) []*Package {
	var out []*Package
	if p, ok := w.prod[pkg]; ok {
		out = append(out, p)
	}
	if p, ok := w.xtest[pkg]; ok {
		out = append(out, p)
	}
	return out
}

// hasMembers reports whether pkg has a Prod and/or XTest package — the
// existence check every membersOf caller that only needs a boolean
// composes on instead of checking len(membersOf(pkg)) > 0.
func (w *Workspace) hasMembers(pkg PackagePath) bool {
	if _, ok := w.prod[pkg]; ok {
		return true
	}
	_, ok := w.xtest[pkg]
	return ok
}

// FileRef pairs a file with the address and kind of the package that owns
// it — the identity Workspace.Files' callers need alongside the file
// itself, since *File carries its Owner but Owner alone doesn't repeat
// the MemberKeys address grouping callers iterate by.
type FileRef struct {
	Pkg  PackagePath
	Kind PackageKind
}
