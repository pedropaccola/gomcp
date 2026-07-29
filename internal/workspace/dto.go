package workspace

import (
	"fmt"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
)

// newDTOSymbol copies a workspace symbol into the shared dto shape.
func newDTOSymbol(s *Symbol) dto.Symbol {
	return dto.Symbol{Key: s.Key(), Kind: dto.SymbolKind(s.Kind), Recv: s.Recv, File: s.File}
}

// newDTOFile copies a workspace file's read-only facts into the shared
// dto shape.
func newDTOFile(f *File) dto.File {
	return dto.File{Path: f.Path, Doc: f.Doc()}
}

// newDTOPackage copies a workspace package's read-only facts into the
// shared dto shape: its files and symbols, translated recursively.
func newDTOPackage(p *Package) dto.Package {
	wf := p.Files()
	files := make([]dto.File, len(wf))
	for i, f := range wf {
		files[i] = newDTOFile(f)
	}
	ws := p.Symbols()
	symbols := make([]dto.Symbol, len(ws))
	for i, s := range ws {
		symbols[i] = newDTOSymbol(s)
	}
	return dto.Package{Path: p.PkgPath, Kind: dto.PackageKind(p.Kind), Doc: p.Doc(), Files: files, Symbols: symbols}
}

// Package resolves a package address (canonical, or a unit's XTest half
// via its own "_test"-suffixed address) to its dto shape.
func (w *Workspace) Package(pkg address.PkgPath) (dto.Package, bool) {
	_, p, _, ok := w.ResolvePackage(pkg)
	if !ok {
		return dto.Package{}, false
	}
	return newDTOPackage(p), true
}

// ExternalPackage resolves a dependency resident in the external cache;
// LoadExternal fills the cache outside a Read call.
func (w *Workspace) ExternalPackage(pkg address.PkgPath) (dto.Package, bool) {
	p, ok := w.LookupExternal(pkg)
	if !ok {
		return dto.Package{}, false
	}
	return newDTOPackage(p), true
}

// Symbol resolves a package address and symbol key ("Name" or "Recv.Name")
// to the symbol and its owning package, checking Prod before XTest before
// falling back to the external dependency cache.
func (w *Workspace) Symbol(pkg address.PkgPath, key string) (dto.Symbol, dto.Package, bool) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return dto.Symbol{}, dto.Package{}, false
	}
	return newDTOSymbol(sym), newDTOPackage(owner), true
}

// Packages enumerates every package in the workspace: addresses in path
// order, Prod before XTest.
func (w *Workspace) Packages() []dto.Package {
	var out []dto.Package
	for _, addr := range w.UnitKeys() {
		unit, _ := w.Unit(addr)
		if prod := unit.Prod(); prod != nil {
			out = append(out, newDTOPackage(prod))
		}
		if xtest := unit.XTest(); xtest != nil {
			out = append(out, newDTOPackage(xtest))
		}
	}
	return out
}

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
