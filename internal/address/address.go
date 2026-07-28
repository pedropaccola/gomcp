// Package address is the shared leaf vocabulary for locating things in the
// workspace: RelativePath (disk-relative file paths) and PkgPath (import
// paths). It depends on nothing else in this module, so workspace, dto,
// gate, engine, and tools each depend on it directly instead of
// re-exporting it for one another.
package address

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// FilePath is a workspace-relative disk path: the address form of the
// disk boundary (files, tombstones, flush, the recheck overlay), while
// PkgPath is the address form of everything else. Untrusted strings enter
// through CleanPath.
type FilePath string

func (p FilePath) String() string {
	return string(p)
}

// RelativePath derives f's on-disk path relative to the workspace root
// — the one representation disk I/O needs (go/packages overlays,
// os.ReadFile/WriteFile). The disk boundary's own door out of FilePath;
// nothing past that boundary should need it.
func (f FilePath) RelativePath(module PkgPath) string {
	rel, _ := strings.CutPrefix(string(f), string(module)+"/")
	return rel
}

// Base is f's bare file name — for presentation alongside a PkgPath shown
// separately, or as the raw material for composing a new FilePath
// (PkgPath.File). Never an address on its own. path.Base, not
// filepath.Base: an address's separator is always "/", regardless of the
// host OS.
func (f FilePath) Base() string {
	return path.Base(string(f))
}

// Dir is the canonical PkgPath of the package f belongs to — valid
// because every FilePath is constructed as pkg+"/"+basename (see
// PkgPath.File). path.Dir, not filepath.Dir: an address's separator is
// always "/", regardless of the host OS.
func (f FilePath) Dir() PkgPath {
	return PkgPath(path.Dir(string(f)))
}

// PkgPath is a package's import path: the canonical address of every
// package, mirroring the type checker's identity. Workspace addresses are
// the module path or prefixed by it; they convert to disk locations only
// at the disk boundary.
type PkgPath string

func (p PkgPath) String() string { return string(p) }

// Join composes a subpackage's PkgPath from an already-known-legitimate
// workspace-relative directory — trusted, no validation, always
// succeeds. For untrusted agent input, use NewPkgPath.
func (p PkgPath) Join(dir string) PkgPath {
	if dir == "" || dir == "." {
		return p
	}
	return PkgPath(p.String() + "/" + dir)
}

// File composes a file's canonical FilePath from an already-known-legitimate
// bare name inside p — trusted, no validation, always succeeds. The
// loader's own door into FilePath construction; for untrusted agent
// input, use NewFilePath.
func (p PkgPath) File(name string) FilePath {
	return FilePath(p.String() + "/" + name)
}

// Base is p's bare final component — the package's own directory name,
// stripped of everything before it (including the module prefix, since
// that's just another leading component). path.Base, not filepath.Base:
// an address's separator is always "/", regardless of the host OS.
func (p PkgPath) Base() string {
	return path.Base(string(p))
}

// cleanRelative narrows s, an untrusted string, against module: absolute
// paths and paths escaping the workspace root are refused; a spelling
// already prefixed by module resolves to its workspace-relative
// remainder, and the bare module root resolves to ".". The shared
// narrowing step behind NewPkgPath and NewFilePath.
func cleanRelative(module PkgPath, s string) (string, bool) {
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

// NewPkgPath narrows addr, an untrusted agent-supplied package address,
// against module: module-prefixed addresses pass through, bare workspace
// directories gain the prefix. File names are refused — packages are
// directories, always spelled alone.
func NewPkgPath(module PkgPath, addr string) (PkgPath, error) {
	path, ok := cleanRelative(module, addr)
	if !ok {
		return "", fmt.Errorf("invalid package path %q", addr)
	}
	if strings.HasSuffix(path, ".go") {
		return "", fmt.Errorf("%q names a file: a package address must name a directory alone", addr)
	}
	if path == "." {
		return module, nil
	}
	return PkgPath(string(module) + "/" + path), nil
}

// NewFilePath narrows raw, an untrusted agent-supplied file address,
// against module and pkg: a bare *.go name is accepted outright and
// joined onto pkg; a path is accepted only when its directory agrees
// with pkg, then narrowed to pkg plus its bare name. Contradictions are
// refused, never guessed.
func NewFilePath(module, pkg PkgPath, raw string) (FilePath, error) {
	name := raw
	if strings.Contains(raw, "/") {
		path, ok := cleanRelative(module, raw)
		if !ok {
			return "", fmt.Errorf("invalid file path %q", raw)
		}
		declaredPkg, err := NewPkgPath(module, filepath.Dir(path))
		if err != nil || declaredPkg != pkg {
			return "", fmt.Errorf("file %q does not live in package %q", raw, pkg)
		}
		name = filepath.Base(path)
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
