package workspace

import (
	"go/build"
	"go/build/constraint"
	"strings"
)

// isBuildExcluded reports whether directives' own //go:build line (if any)
// evaluates false against the host's build configuration — the same
// evaluation gopls's own standaloneTags mechanism does, and the one
// go/packages already ran once, upstream, to decide whether this file
// belonged in IgnoredFiles in the first place. Uses go/build.Default's
// GOOS/GOARCH/ReleaseTags/CgoEnabled directly, matching gomcp's own
// no-override default. Known limitation: doesn't special-case the
// synthesized "unix" umbrella tag or less common build tags outside those
// four categories — best effort, not full parity with go/build's own
// (unexported) matching, same posture as everywhere else directives are
// interpreted rather than just stored.
func isBuildExcluded(directives []string) bool {
	tags := make(map[string]bool, len(build.Default.ReleaseTags)+3)
	tags[build.Default.GOOS] = true
	tags[build.Default.GOARCH] = true
	if build.Default.CgoEnabled {
		tags["cgo"] = true
	}
	for _, t := range build.Default.ReleaseTags {
		tags[t] = true
	}
	for _, d := range directives {
		rest, ok := strings.CutPrefix(d, "go:build ")
		if !ok {
			continue
		}
		expr, err := constraint.Parse("//go:build " + rest)
		if err != nil {
			continue // malformed constraint: don't guess, leave classification as-is
		}
		if !expr.Eval(func(tag string) bool { return tags[tag] }) {
			return true
		}
	}
	return false
}

// ensureMemberInstalled installs a fresh, empty Prod or XTest package
// shell at pkg if one isn't already there — the shell InstallFileAtDirectiveKind
// needs before its first SwapFile into a shape that had no prior member
// at this address.
func (w *Workspace) ensureMemberInstalled(pkg PackagePath, kind PackageKind, name string) {
	switch kind {
	case KindXTest:
		if _, ok := w.xtest[pkg]; !ok {
			w.InstallXTest(pkg, NewPackage(name, pkg, KindXTest, nil, nil))
		}
	default:
		if _, ok := w.prod[pkg]; !ok {
			w.InstallProd(pkg, NewPackage(name, pkg, KindProd, nil, nil))
		}
	}
}

// ReclassifyFile updates path's Ignored bit to whatever its own
// (possibly just-edited) directives now say — a file edited into (or
// out of) build-exclusion is marked accordingly, in place. Shape
// (Prod/XTest) never changes here: a //go:build edit never changes a
// file's own package clause, so kind is passed straight through
// unmodified. Returns whether the file is now Ignored.
func (w *Workspace) ReclassifyFile(pkg PackagePath, path FilePath, kind PackageKind, directives []string, content []byte) (bool, error) {
	ignored := isBuildExcluded(directives)
	if err := w.SwapFile(pkg, kind, ignored, path, content); err != nil {
		return false, err
	}
	return ignored, nil
}

// InstallFileAtDirectiveKind installs content at path within pkg's
// requestedKind (Prod or XTest) half, stamped Ignored per whatever
// //go:build directive content's own leading comment carries — a file
// created with a directive that excludes it from the current build
// still lands in the shape the agent asked for, just marked Ignored,
// never diverted to a different half the way the old Ignored-bucket
// design required.
func (w *Workspace) InstallFileAtDirectiveKind(pkg PackagePath, path FilePath, requestedKind PackageKind, ownerName string, directives []string, content []byte) error {
	return w.SwapFile(pkg, requestedKind, isBuildExcluded(directives), path, content)
}
