package workspace

import (
	"fmt"
	"go/parser"
	"go/token"
	"maps"

	"golang.org/x/tools/imports"
)

// Clone copies the mutable model for a transaction: prod, xtest,
// packages, and the tombstone map start shared with the original and
// fork lazily, only for what this transaction actually touches
// (ensureProdForked, ensureXTestForked, ensureRemovedForked,
// ensurePackageForked) — position tables and the dependency cache are
// shared outright (both are append-only within a transaction's
// lifetime). Edit works on the clone and discards it on error — error
// means nothing happened.
func (w *Workspace) Clone() *Workspace {
	cloned := *w
	cloned.prodForked = false
	cloned.xtestForked = false
	cloned.removedForked = false
	cloned.forkedPkgs = nil
	return &cloned
}

// SwapFile is the one way file content enters the model on the mutation
// path: goimports-format the candidate bytes against newPath's own
// address (no real filesystem is consulted — goimports classifies
// imports purely from the filename's shape), parse the formatted
// result, then — only once both succeed — fork the owning package and
// install via Package.SwapFile. ignored stamps the installed File's own
// Ignored bit; it never affects which of Prod/XTest's own map the file
// lands in — kind (shape) and ignored (build participation) are
// independent. Every fallible step precedes the fork — an error means
// the model is untouched.
func (w *Workspace) SwapFile(addr PackagePath, kind PackageKind, ignored bool, newPath FilePath, src []byte) error {
	formatted, err := imports.Process(newPath.String(), src, nil)
	if err != nil {
		return fmt.Errorf("%s does not format: %w", newPath, err)
	}
	astFile, err := parser.ParseFile(w.fset, newPath.String(), formatted, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("%s does not parse: %w", newPath, err)
	}
	pkg := w.ensurePackageForked(addr, kind)
	pkg.SwapFile(newPath, ignored, formatted, astFile)
	w.ensureRemovedForked()
	delete(w.removed, newPath)
	return nil
}

// DropFile removes one file from its owner: tombstoned for the disk
// boundary, index rebuilt, and its members pruned once the last file is
// gone — an address with no files is no address.
func (w *Workspace) DropFile(addr PackagePath, kind PackageKind, path FilePath) {
	owner := w.ensurePackageForked(addr, kind)
	delete(owner.files, path)
	owner.RebuildIndex()
	w.tombstone(addr, path, owner.Name)
	w.pruneEmptyMembers(addr)
}

// MoveFile relocates a file within its owner — semantically free in Go,
// files are storage. The old path is tombstoned, the new one untombstoned,
// and the moved copy marked dirty for the next flush.
func (w *Workspace) MoveFile(addr PackagePath, kind PackageKind, oldPath, newPath FilePath) {
	owner := w.ensurePackageForked(addr, kind)
	file := owner.files[oldPath]
	moved := *file
	moved.Path = newPath
	moved.dirty = true
	delete(owner.files, oldPath)
	owner.files[newPath] = &moved
	w.tombstone(addr, oldPath, owner.Name)
	w.ClearTombstone(newPath)
	owner.RebuildIndex()
}

// pruneEmptyMembers drops pkg's Prod and XTest packages, independently,
// once each is out of files — an address with no files in any map is no
// address.
func (w *Workspace) pruneEmptyMembers(pkg PackagePath) {
	pruneIfEmpty(w.prod, pkg)
	pruneIfEmpty(w.xtest, pkg)
}

// ForkExternal returns a shallow copy of w with fresh, independent
// external and externalErr maps seeded from the current ones — safe for
// LoadExternal to mutate without racing a reader still holding an older
// generation that shares this Workspace's dependency cache. Everything
// else (units, removed, fset, module) stays shared, since LoadExternal
// never touches them.
func (w *Workspace) ForkExternal() *Workspace {
	forked := *w
	forked.external = maps.Clone(w.external)
	forked.externalErr = maps.Clone(w.externalErr)
	return &forked
}

// ensureProdForked forks the Prod map (one maps.Clone) the first time
// this generation installs or removes a Prod package; idempotent after
// that. Forking the map is separate from forking one package's own
// contents (ensurePackageForked) — most mutations only need the latter.
func (w *Workspace) ensureProdForked() {
	if w.prodForked {
		return
	}
	w.prod = maps.Clone(w.prod)
	w.prodForked = true
}

// ensureRemovedForked forks the tombstone map the first time this
// generation tombstones or clears a path; idempotent after that.
func (w *Workspace) ensureRemovedForked() {
	if w.removedForked {
		return
	}
	w.removed = maps.Clone(w.removed)
	w.removedForked = true
}

// ensurePackageForked returns the Prod or XTest package at addr, forking
// it the first time this generation mutates it; every other package's
// pointer stays shared with whatever generation this one was cloned
// from. addr must already be installed in the relevant map —
// CreatePackage and MovePackage install into it before calling this,
// precisely so it always is.
func (w *Workspace) ensurePackageForked(addr PackagePath, kind PackageKind) *Package {
	switch kind {
	case KindXTest:
		w.ensureXTestForked()
	default:
		w.ensureProdForked()
	}
	var m map[PackagePath]*Package
	switch kind {
	case KindXTest:
		m = w.xtest
	default:
		m = w.prod
	}
	pkg := m[addr]
	if w.forkedPkgs[pkg] {
		return pkg
	}
	forked := pkg.Clone()
	m[addr] = forked
	if w.forkedPkgs == nil {
		w.forkedPkgs = make(map[*Package]bool)
	}
	w.forkedPkgs[forked] = true
	return forked
}

// MarkFlushed clears path's dirty mark on the package at addr — Flush's
// half of the dirty lifecycle; SwapFile and MoveFile set the mark. Forks
// the package first if this generation hasn't already, same as every
// other mutating primitive.
func (w *Workspace) MarkFlushed(addr PackagePath, kind PackageKind, path FilePath) {
	w.ensurePackageForked(addr, kind).MarkFlushed(path)
}

// CreatePackage creates a new package half (Prod, or XTest when isXTest)
// at a module-prefixed address with one stub file named after the
// package (name defaults to the address base, plus "_test" for the XTest
// half). Fails if that specific half already exists at the address, is
// outside the module, or name isn't a valid identifier — XTest can be
// created with no Prod sibling present, and vice versa: an agent may
// legitimately write the test before the implementation, and this verb
// doesn't assume the reverse order or fabricate the other half to paper
// over it. The stub file is never Ignored — a freshly created file has
// no directives yet. Returns the stub file's path.
func (w *Workspace) CreatePackage(pkg PackagePath, name string, isXTest bool) (FilePath, error) {
	if pkg == w.module {
		return "", OutsideModuleCreateError(pkg, w.module)
	}
	kind := KindProd
	_, exists := w.prod[pkg]
	if isXTest {
		kind = KindXTest
		_, exists = w.xtest[pkg]
	}
	if exists {
		return "", PackageExistsError(pkg)
	}
	if name == "" {
		name = pkg.Base()
		if isXTest {
			name += "_test"
		}
	}
	if !token.IsIdentifier(name) {
		return "", InvalidPackageNameError(name)
	}
	if isXTest {
		w.InstallXTest(pkg, NewPackage(name, pkg, KindXTest, nil, nil))
	} else {
		w.InstallProd(pkg, NewPackage(name, pkg, KindProd, nil, nil))
	}
	path := pkg.File(name + ".go")
	if err := w.SwapFile(pkg, kind, false, path, []byte("package "+name+"\n")); err != nil {
		return "", err
	}
	return path, nil
}

// DropPackage removes a whole package address at once: every member
// file tombstoned, then the address's own members unmapped — the
// efficient counterpart to tombstoning each file through DropFile, which
// would also rebuild the index and prune per file for an address about
// to be discarded wholesale regardless. Idempotent: a missing package is
// a noop, not a failure.
func (w *Workspace) DropPackage(pkg PackagePath) []FilePath {
	members := w.MembersOf(pkg)
	if len(members) == 0 {
		return nil
	}
	var touched []FilePath
	for _, p := range members {
		for _, file := range p.Files() {
			w.tombstone(pkg, file.Path, p.Name)
			touched = append(touched, file.Path)
		}
	}
	w.removeMembers(pkg)
	return touched
}

// ensureXTestForked is ensureProdForked's XTest sibling, same rationale.
func (w *Workspace) ensureXTestForked() {
	if w.xtestForked {
		return
	}
	w.xtest = maps.Clone(w.xtest)
	w.xtestForked = true
}

// CreateFile adds an empty file to an existing package half (Prod, or
// XTest when kind is KindXTest), optionally seeded with a package doc
// comment and/or leading compiler directives (//go:build, //go:generate,
// //go:embed — no space after "//"). The target half must already exist
// — CreatePackage creates it first; this verb creates only the file,
// never a whole package implicitly. A //go:build directive that
// excludes the new file from the current build installs it Ignored
// instead of active — see InstallFileAtDirectiveKind. Returns the file
// created, for the caller's own change-tracking.
func (w *Workspace) CreateFile(pkg PackagePath, kind PackageKind, name, doc string, directives []string) (FilePath, error) {
	var target *Package
	for _, p := range w.MembersOf(pkg) {
		if p.ID.Kind() == kind {
			target = p
			break
		}
	}
	if target == nil {
		return "", NoPackageError(pkg)
	}
	path, err := NewFilePath(w.Module(), pkg, name)
	if err != nil {
		return "", err
	}
	if _, _, exists := w.ResolveFileByPath(path); exists {
		return "", fmt.Errorf("file %q already exists", path)
	}
	content := string(RenderDirectives(directives)) + string(RenderDocComment(doc)) + "package " + target.Name + "\n"
	if err := w.InstallFileAtDirectiveKind(pkg, path, kind, target.Name, directives, []byte(content)); err != nil {
		return "", err
	}
	return path, nil
}

// DropTombstonedFile removes path from freshly loaded Prod/XTest maps —
// the load-path counterpart of DropFile: overlays can only mask a
// deleted file as empty, so the mask's residue must not survive as a
// real file. Emptied packages are pruned the way pruneEmptyMembers prunes
// installed ones.
func DropTombstonedFile(prod, xtest map[PackagePath]*Package, pkg PackagePath, path FilePath) {
	for _, m := range []map[PackagePath]*Package{prod, xtest} {
		p, ok := m[pkg]
		if !ok {
			continue
		}
		if _, ok := p.files[path]; ok {
			delete(p.files, path)
			p.RebuildIndex()
		}
		pruneIfEmpty(m, pkg)
	}
}

// pruneIfEmpty drops pkg from m once its package is out of files. Shared
// by pruneEmptyMembers (an installed workspace) and DropTombstonedFile (a
// freshly loaded map, before installation) — called once per map (Prod,
// then XTest) by each.
func pruneIfEmpty(m map[PackagePath]*Package, pkg PackagePath) {
	if p, ok := m[pkg]; ok && len(p.files) == 0 {
		delete(m, pkg)
	}
}

// MarkFileDirty re-marks path dirty in whichever of pkg's Prod/XTest
// packages holds it, within freshly loaded (not yet installed) maps —
// how dirty state survives a reload built from overlays. Replaces
// rather than mutates in place, since a File may still be shared with
// another Workspace generation via Clone.
func MarkFileDirty(prod, xtest map[PackagePath]*Package, pkg PackagePath, path FilePath) {
	for _, m := range []map[PackagePath]*Package{prod, xtest} {
		p, ok := m[pkg]
		if !ok {
			continue
		}
		if file, ok := p.files[path]; ok {
			cp := *file
			cp.dirty = true
			p.files[path] = &cp
		}
	}
}
