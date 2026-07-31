package workspace

import (
	"go/token"
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

// EnsureXTest is ResolvePackage's create-side sibling: when id names
// a unit's XTest half that doesn't exist yet, it installs a fresh XTest
// package — and, if the unit doesn't exist at all yet, a fresh Prod
// sibling too, so a create verb never needs the destination package to
// already exist first just to target a brand-new package's XTest half.
// freshProd is non-nil exactly when a Prod shell had to be originated
// here: the caller (which alone can install real file content) must
// give it a stub file within the same transaction, the same way
// CreatePackage always pairs a fresh shell with a real file. The one
// door a create verb is allowed to originate a package that isn't there
// yet, mirroring CreatePackage's own shell construction for a brand new
// unit.
func (w *Workspace) EnsureXTest(id PackageID) (pkg, freshProd *Package, err error) {
	if p, ok := w.ResolvePackage(id); ok {
		return p, nil, nil
	}
	if id.Kind() != KindXTest {
		return nil, nil, NoPackageError(id)
	}
	base := id.Base()
	if base == w.module {
		return nil, nil, OutsideModuleCreateError(base, w.module)
	}
	unit, ok := w.Unit(base)
	var prod *Package
	if ok {
		prod = unit.Prod()
	}
	if prod == nil {
		name := base.Base()
		if !token.IsIdentifier(name) {
			return nil, nil, InvalidPackageNameError(name)
		}
		prod = NewPackage(name, base, KindProd, nil, nil)
		freshProd = prod
	}
	fresh := NewPackage(prod.Name+"_test", base, KindXTest, nil, nil)
	w.InstallUnit(base, NewUnit(prod, fresh))
	return fresh, freshProd, nil
}
