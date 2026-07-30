package workspace

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// PackagePath is a package's canonical import path — always unsuffixed,
// by construction: nothing produces a PackagePath carrying go/packages'
// own "_test" convention for an external-test half. This is what Unit
// is keyed by, what Workspace's module root is, and the "path"
// component sealed inside PackageID — a Unit is inherently kind-agnostic
// (it bundles both a Prod and an XTest Package at one directory), so its
// own key can never meaningfully carry a kind.
type PackagePath string

func (p PackagePath) String() string { return string(p) }

// Join composes a subpackage's PackagePath from an already-known-legitimate
// workspace-relative directory — trusted, no validation, always
// succeeds. For untrusted agent input, use NewPackageID.
func (p PackagePath) Join(dir string) PackagePath {
	if dir == "" || dir == "." {
		return p
	}
	return PackagePath(p.String() + "/" + dir)
}

// Base is p's bare final component — the package's own directory name,
// stripped of everything before it (including the module prefix, since
// that's just another leading component). path.Base, not filepath.Base:
// an address's separator is always "/", regardless of the host OS.
func (p PackagePath) Base() string {
	return path.Base(string(p))
}

// PackageKind classifies what a package is relative to the workspace:
// its own production or external-test half, or a read-only dependency
// from the module cache. Mutually exclusive by construction — closes the
// illegal state IsXTest and External as two independent bools allowed
// (both true simultaneously), even though no code path ever produced it.
type PackageKind int

const (
	KindProd PackageKind = iota
	KindXTest
	KindExternal
)

var packageKindNames = [...]string{"prod", "xtest", "external"}

// File composes a file's FilePath from an already-known-legitimate bare
// name inside p — trusted, no validation, always succeeds. Every
// FilePath is built from a canonical PackagePath alone: a file's own
// address never carries the XTest distinction, since internal_test.go
// and external_test.go can live in the identical directory, addressed
// the same way — only the file's own package clause says which half
// owns it.
func (p PackagePath) File(name string) FilePath {
	return FilePath(p.String() + "/" + name)
}

// String returns k's lowercase name.
func (k PackageKind) String() string {
	return packageKindNames[k]
}

// PackageID names one specific package variant — Prod, XTest, or
// External — never constructible in a state where its own path and kind
// could disagree, since NewPackageID (untrusted, agent-supplied text)
// and newPackageID (trusted, already-known-good pieces) are the only
// doors. This is the type that replaces a bare address everywhere a
// mismatched-suffix bug was possible: Package.ID, and every store/tools
// signature that names one resolved-or-being-resolved package.
type PackageID struct {
	path PackagePath
	kind PackageKind
}

// Kind reports whether id names a Prod, XTest, or External package.
func (id PackageID) Kind() PackageKind { return id.kind }

// Base is id's canonical path, kind stripped — for Unit lookups and file
// construction, which are inherently kind-agnostic.
func (id PackageID) Base() PackagePath { return id.path }

// String recomposes id's full spelling — go/packages' own "_test"
// suffix convention on an XTest half, the bare path otherwise. The
// external contract every tool's JSON output already uses.
func (id PackageID) String() string {
	if id.kind == KindXTest {
		return string(id.path) + "_test"
	}
	return string(id.path)
}

// FilePath is a workspace-relative disk path: the address form of the
// disk boundary (files, tombstones, flush, the recheck overlay), while
// PackagePath/PackageID are the address forms of everything else.
// Ownership isn't derivable from a file's own address alone —
// internal_test.go and external_test.go can live in the identical
// directory, addressed the same way; only parsing the file's own package
// clause tells you which package owns it. Untrusted strings enter
// through NewFilePath.
type FilePath string

func (f FilePath) String() string { return string(f) }

// RelativePath derives f's on-disk path relative to the workspace root
// — the one representation disk I/O needs (go/packages overlays,
// os.ReadFile/WriteFile). The disk boundary's own door out of FilePath;
// nothing past that boundary should need it.
func (f FilePath) RelativePath(module PackagePath) string {
	rel, _ := strings.CutPrefix(string(f), string(module)+"/")
	return rel
}

// Base is f's bare file name — for presentation alongside a package
// address shown separately, or as the raw material for composing a new
// FilePath (PackagePath.File). Never an address on its own. path.Base,
// not filepath.Base: an address's separator is always "/", regardless
// of the host OS.
func (f FilePath) Base() string {
	return path.Base(string(f))
}

// PackagePath is the canonical package address f belongs to. Always
// already the exact address that built f (every FilePath is constructed
// as pkg+"/"+basename — see PackagePath.File), never independently
// derived or canonicalized — there is no XTest-suffixed shape for this
// to strip, since a file's own address never carries that distinction.
// path.Dir, not filepath.Dir: an address's separator is always "/",
// regardless of the host OS.
func (f FilePath) PackagePath() PackagePath {
	return PackagePath(path.Dir(string(f)))
}

// newPackageID builds an already-validated identity directly from a
// canonical path and kind — the trusted door used only by workspace's
// own construction paths (NewPackage, disk's ingestion), which already
// know both pieces agree. Narrowing untrusted agent text belongs to
// NewPackageID alone.
func newPackageID(path PackagePath, kind PackageKind) PackageID {
	return PackageID{path: path, kind: kind}
}

// NewPackageID narrows addr, an untrusted agent-supplied package
// address, against module: module-prefixed addresses pass through, bare
// workspace directories gain the prefix, and go/packages' own "_test"
// suffix convention for an external-test half is split off into Kind
// here, once, so path and kind can never independently drift apart
// afterward. File names are refused — packages are directories, always
// spelled alone.
func NewPackageID(module PackagePath, addr string) (PackageID, error) {
	rel, ok := cleanRelative(module, addr)
	if !ok {
		return PackageID{}, fmt.Errorf("invalid package path %q", addr)
	}
	if strings.HasSuffix(rel, ".go") {
		return PackageID{}, fmt.Errorf("%q names a file: a package address must name a directory alone", addr)
	}
	full := string(module)
	if rel != "." {
		full = string(module) + "/" + rel
	}
	kind := KindProd
	if trimmed, isXTest := strings.CutSuffix(full, "_test"); isXTest {
		full, kind = trimmed, KindXTest
	}
	return PackageID{path: PackagePath(full), kind: kind}, nil
}

// cleanRelative narrows s, an untrusted string, against module: absolute
// paths and paths escaping the workspace root are refused; a spelling
// already prefixed by module resolves to its workspace-relative
// remainder, and the bare module root resolves to ".". The shared
// narrowing step behind NewPackageID and NewFilePath.
func cleanRelative(module PackagePath, s string) (string, bool) {
	p := filepath.Clean(s)
	if filepath.IsAbs(p) || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", false
	}
	if p == string(module) {
		return ".", true
	}
	if trimmed := strings.TrimPrefix(p, string(module)+"/"); trimmed != p {
		return trimmed, true
	}
	return p, true
}

// NewFilePath narrows raw, an untrusted agent-supplied file address,
// against module and pkg: a bare *.go name is accepted outright and
// joined onto pkg; a path is accepted only when its directory agrees
// with pkg, then narrowed to pkg plus its bare name. pkg is always the
// canonical, kind-stripped form — PackageID.Base() when the caller holds
// a resolved identity — since a file's address never carries the XTest
// distinction. Contradictions are refused, never guessed.
func NewFilePath(module, pkg PackagePath, raw string) (FilePath, error) {
	name := raw
	if strings.Contains(raw, "/") {
		rel, ok := cleanRelative(module, raw)
		if !ok {
			return "", fmt.Errorf("invalid file path %q", raw)
		}
		declaredPkg, err := NewPackageID(module, filepath.Dir(rel))
		if err != nil || declaredPkg.Base() != pkg {
			return "", fmt.Errorf("file %q does not live in package %q", raw, pkg)
		}
		name = filepath.Base(rel)
	}
	if !strings.HasSuffix(name, ".go") {
		return "", fmt.Errorf("file name must be a bare *.go name, got %q", name)
	}
	return pkg.File(name), nil
}

// IsOutsideRoot reports whether a workspace-relative path (already
// cleaned) points outside the workspace root.
func IsOutsideRoot(p string) bool {
	return p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator))
}
