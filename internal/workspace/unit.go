package workspace

import "github.com/pedropaccola/gomcp/internal/address"

// Unit holds the packages of one workspace address: the production package
// (with in-package test files folded in) and the external _test package.
type Unit struct {
	Prod  *Package
	XTest *Package
}

// MarkDirty re-marks path dirty in whichever of the unit's packages holds
// it — how dirty state survives a reload built from overlays. Replaces
// rather than mutates in place, since a File may still be shared with
// another Workspace generation via Clone.
func (u *Unit) MarkDirty(path address.RelativePath) {
	for _, p := range []*Package{u.Prod, u.XTest} {
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
