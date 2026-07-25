// Package address is the shared leaf vocabulary for locating things in the
// workspace: RelativePath (disk-relative file paths) and PkgPath (import
// paths). It depends on nothing else in this module, so workspace, dto,
// gate, engine, and tools each depend on it directly instead of
// re-exporting it for one another.
package address

import (
	"path/filepath"
	"strings"
)

// RelativePath is a workspace-relative disk path: the address form of the
// disk boundary (files, tombstones, flush, the recheck overlay), while
// PkgPath is the address form of everything else. Untrusted strings enter
// through CleanPath.
type RelativePath string

// IsOutsideRoot reports whether the path points outside the workspace root.
func (p RelativePath) IsOutsideRoot() bool {
	return p == ".." || strings.HasPrefix(string(p), ".."+string(filepath.Separator))
}

// Base is the final path element: the bare file name for file paths.
func (p RelativePath) Base() string {
	return filepath.Base(string(p))
}

// Clean re-normalizes the path so equivalent spellings of the same address
// ("./x", "x/", "a//b") resolve identically. Resolvers apply it on entry.
func (p RelativePath) Clean() RelativePath {
	return RelativePath(filepath.Clean(string(p)))
}

// Dir is the path of the containing directory ("." for root-level paths).
func (p RelativePath) Dir() RelativePath {
	return RelativePath(filepath.Dir(string(p)))
}

// Join appends a name to the path.
func (p RelativePath) Join(name string) RelativePath {
	return RelativePath(filepath.Join(string(p), name))
}

func (p RelativePath) String() string {
	return string(p)
}

// PkgPath is a package's import path: the canonical address of every
// package, mirroring the type checker's identity. Workspace addresses are
// the module path or prefixed by it; they convert to disk locations only
// at the disk boundary.
type PkgPath string

func (p PkgPath) String() string { return string(p) }

// CleanPath is the constructor for untrusted path strings (agent input):
// it normalizes equivalent spellings and rejects addresses that cannot
// live inside the workspace — absolute paths and paths escaping the root.
func CleanPath(s string) (RelativePath, bool) {
	p := RelativePath(filepath.Clean(s))
	if filepath.IsAbs(string(p)) || p.IsOutsideRoot() {
		return "", false
	}
	return p, true
}
