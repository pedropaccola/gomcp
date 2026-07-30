package workspace

import (
	"fmt"
)

// ResolvePackage resolves id to the specific package it names — Prod or
// XTest, per id.Kind() — within its unit.
func (w *Workspace) ResolvePackage(id PackageID) (*Package, bool) {
	unit, ok := w.Unit(id.Base())
	if !ok {
		return nil, false
	}
	if id.Kind() == KindXTest {
		if p := unit.XTest(); p != nil {
			return p, true
		}
		return nil, false
	}
	if p := unit.Prod(); p != nil {
		return p, true
	}
	return nil, false
}

// EnsurePackage is ResolvePackage's create-side sibling: when id names
// a unit's XTest half that doesn't exist yet (its Prod sibling must
// already), it installs a fresh XTest package instead of failing — name
// and address following the same convention go/packages itself gives
// that half. The one door a create verb is allowed to originate a
// package that isn't there yet, mirroring CreatePackage's own shell
// construction for a brand new unit.
func (w *Workspace) EnsurePackage(id PackageID) (*Package, error) {
	if pkg, ok := w.ResolvePackage(id); ok {
		return pkg, nil
	}
	if id.Kind() != KindXTest {
		return nil, fmt.Errorf("no package at %q: create_package first", id)
	}
	unit, ok := w.Unit(id.Base())
	if !ok || unit.Prod() == nil {
		return nil, fmt.Errorf("no package at %q: create_package first", id.Base())
	}
	fresh := NewPackage(unit.Prod().Name+"_test", id.Base(), KindXTest, nil, nil)
	w.InstallUnit(id.Base(), NewUnit(unit.Prod(), fresh))
	return fresh, nil
}
