package workspace

import "github.com/pedropaccola/gomcp/internal/address"

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
	for _, p := range []*Package{u.prod, u.xtest} {
		if p == nil {
			continue
		}
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

// NewUnit assembles a Unit from its two halves atomically — the only
// door: nothing outside this package ever sees a Unit that wasn't built
// with both halves already decided, so there's no window where a
// half-built pairing could be observed or published.
func NewUnit(prod, xtest *Package) *Unit {
	return &Unit{prod: prod, xtest: xtest}
}
