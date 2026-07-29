package workspace

import (
	"iter"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
)

// Unit holds the packages of one workspace address: the production package
// (with in-package test files folded in) and the external _test package.
// Both are unexported so the compiler, not convention, keeps a Unit from
// ever being half-built by code outside this package — NewUnit is the
// only door.
type Unit struct {
	prod  *Package
	xtest *Package
}

// MarkDirty re-marks path dirty in whichever of the unit's packages holds
// it — how dirty state survives a reload built from overlays. Replaces
// rather than mutates in place, since a File may still be shared with
// another Workspace generation via Clone.
func (u *Unit) MarkDirty(path address.FilePath) {
	for _, p := range u.Members() {
		if file, ok := p.files[path]; ok {
			cp := *file
			cp.dirty = true
			p.files[path] = &cp
		}
	}
}

// Prod is the unit's production package, nil if it has none.
func (u *Unit) Prod() *Package {
	return u.prod
}

// XTest is the unit's external _test package, nil if it has none.
func (u *Unit) XTest() *Package {
	return u.xtest
}

// Dir is the canonical PkgPath of the directory this unit's packages
// physically occupy — always the production-shaped address, even when
// only the XTest half exists (its own PkgPath carries a "_test" suffix
// the directory itself does not).
func (u *Unit) Dir() address.PkgPath {
	if u.prod != nil {
		return u.prod.PkgPath
	}
	return address.PkgPath(strings.TrimSuffix(string(u.xtest.PkgPath), "_test"))
}

// Members returns the unit's non-nil packages, Prod before XTest — 1 or 2
// entries, never both nil (a Unit with neither is pruned). Compacted: a
// caller that needs to know which entry is which compares against XTest()
// directly (identity, not position) rather than trusting an index, since
// the position of a given half shifts once the other is absent.
func (u *Unit) Members() []*Package {
	var out []*Package
	if u.prod != nil {
		out = append(out, u.prod)
	}
	if u.xtest != nil {
		out = append(out, u.xtest)
	}
	return out
}

// Files yields every file across the unit's member packages, paired with
// whether it belongs to the XTest half — the walk otherwise hand-rolled
// over Members()/Package.Files() at every caller that needs it.
func (u *Unit) Files() iter.Seq2[bool, *File] {
	return func(yield func(bool, *File) bool) {
		for _, pkg := range u.Members() {
			for _, file := range pkg.Files() {
				if !yield(pkg.IsXTest, file) {
					return
				}
			}
		}
	}
}

// NewUnit assembles a Unit from its two halves atomically — the only
// door: nothing outside this package ever sees a Unit that wasn't built
// with both halves already decided, so there's no window where a
// half-built pairing could be observed or published.
func NewUnit(prod, xtest *Package) *Unit {
	return &Unit{prod: prod, xtest: xtest}
}
