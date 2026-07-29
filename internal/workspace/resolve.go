package workspace

import (
	"fmt"

	"github.com/pedropaccola/gomcp/internal/address"
)

// ResolvePackage resolves addr to its unit's canonical address and the
// specific package it names — Prod, or (via address.PkgPath.IsXTest/
// Canon) its XTest half.
func (w *Workspace) ResolvePackage(addr address.PkgPath) (canon address.PkgPath, pkg *Package, isXTest bool, ok bool) {
	canon = addr.Canon()
	unit, found := w.Unit(canon)
	if !found {
		return "", nil, false, false
	}
	if addr == canon {
		if p := unit.Prod(); p != nil {
			return canon, p, false, true
		}
		return "", nil, false, false
	}
	if p := unit.XTest(); p != nil {
		return canon, p, true, true
	}
	return "", nil, false, false
}

// EnsurePackage is ResolvePackage's create-side sibling: when addr names
// a unit's XTest half that doesn't exist yet (its Prod sibling must
// already), it installs a fresh XTest package instead of failing — name
// and address following the same convention go/packages itself gives
// that half. The one door a create verb is allowed to originate a
// package that isn't there yet, mirroring CreatePackage's own shell
// construction for a brand new unit.
func (w *Workspace) EnsurePackage(addr address.PkgPath) (canon address.PkgPath, pkg *Package, isXTest bool, err error) {
	if canon, pkg, isXTest, ok := w.ResolvePackage(addr); ok {
		return canon, pkg, isXTest, nil
	}
	canon = addr.Canon()
	if canon == addr {
		return "", nil, false, fmt.Errorf("no package at %q: create_package first", addr)
	}
	unit, ok := w.Unit(canon)
	if !ok || unit.Prod() == nil {
		return "", nil, false, fmt.Errorf("no package at %q: create_package first", canon)
	}
	fresh := NewPackage(unit.Prod().Name+"_test", addr, nil, nil, KindXTest)
	w.InstallUnit(canon, NewUnit(unit.Prod(), fresh))
	return canon, fresh, true, nil
}
