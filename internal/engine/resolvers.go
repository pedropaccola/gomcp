package engine

import (
	"go/token"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// Module is the workspace's module path: the prefix of every workspace
// package address. Valid inside Read, where the snapshot is held.
func (v *View) Module() address.PkgPath {
	return v.ws.Module()
}

// resolvePackage resolves a canonical package address to its production
// package, in the workspace's own model type — the internal resolver real
// work (splicing, type lookups) composes on.
func (v *View) resolvePackage(pkg address.PkgPath) (*workspace.Package, bool) {
	unit, ok := v.ws.Unit(pkg)
	if !ok || unit.Prod == nil {
		return nil, false
	}
	return unit.Prod, true
}

// resolveXTest resolves a canonical package address to its external test
// package, in the workspace's own model type.
func (v *View) resolveXTest(pkg address.PkgPath) (*workspace.Package, bool) {
	unit, ok := v.ws.Unit(pkg)
	if !ok || unit.XTest == nil {
		return nil, false
	}
	return unit.XTest, true
}

// resolveExternal resolves a dependency resident in the external cache, in
// the workspace's own model type; LoadExternal fills the cache outside the
// read gate.
func (v *View) resolveExternal(pkg address.PkgPath) (*workspace.Package, bool) {
	return v.ws.ExternalPackage(pkg)
}

// resolveFile resolves a file path to the file and its owning package, in
// the workspace's own model types, checking the production package before
// the external test package. Dependency files resolve through their
// import-path-qualified pseudo-paths.
func (v *View) resolveFile(path address.RelativePath) (*workspace.File, *workspace.Package, bool) {
	path = path.Clean()
	if unit, ok := v.ws.Unit(v.pkgAt(path.Dir())); ok {
		for _, pkg := range []*workspace.Package{unit.Prod, unit.XTest} {
			if pkg == nil {
				continue
			}
			if file, ok := pkg.File(path); ok {
				return file, pkg, true
			}
		}
	}
	if pkg, ok := v.ws.ExternalPackage(address.PkgPath(path.Dir())); ok {
		if file, ok := pkg.File(path); ok {
			return file, pkg, true
		}
	}
	return nil, nil, false
}

// resolveSymbol resolves a package address and symbol key ("Name" or
// "Recv.Name") to the symbol and its owning package, in the workspace's
// own model types, checking Prod before XTest before falling back to the
// external cache — the one resolver every address-based lookup composes
// on, so dependency symbols work everywhere a workspace symbol does.
func (v *View) resolveSymbol(pkg address.PkgPath, key string) (*workspace.Symbol, *workspace.Package, bool) {
	if unit, ok := v.ws.Unit(pkg); ok {
		for _, p := range []*workspace.Package{unit.Prod, unit.XTest} {
			if p == nil {
				continue
			}
			if sym, ok := p.Symbol(key); ok {
				return sym, p, true
			}
		}
	}
	if p, ok := v.ws.ExternalPackage(pkg); ok {
		if sym, ok := p.Symbol(key); ok {
			return sym, p, true
		}
	}
	return nil, nil, false
}

// Package resolves a canonical package address to its production package.
func (v *View) Package(pkg address.PkgPath) (Package, bool) {
	p, ok := v.resolvePackage(pkg)
	if !ok {
		return Package{}, false
	}
	return newPackage(p), true
}

// ExternalPackage resolves a dependency resident in the external cache;
// LoadExternal fills the cache outside the read gate.
func (v *View) ExternalPackage(pkg address.PkgPath) (Package, bool) {
	p, ok := v.resolveExternal(pkg)
	if !ok {
		return Package{}, false
	}
	return newPackage(p), true
}

// Symbol resolves a package address and symbol key ("Name" or "Recv.Name")
// to the symbol and its owning package, checking Prod before XTest.
func (v *View) Symbol(pkg address.PkgPath, key string) (Symbol, Package, bool) {
	sym, owner, ok := v.resolveSymbol(pkg, key)
	if !ok {
		return Symbol{}, Package{}, false
	}
	return newSymbol(sym), newPackage(owner), true
}

// dirOf unwraps a workspace package address to its directory, comma-ok
// false outside the module: dependencies have no workspace location.
func (v *View) dirOf(pkg address.PkgPath) (address.RelativePath, bool) {
	if pkg == v.ws.Module() {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(string(pkg), string(v.ws.Module())+"/"); ok {
		return address.RelativePath(rest), true
	}
	return "", false
}

// pkgAt wraps a workspace directory into its canonical package address.
func (v *View) pkgAt(dir address.RelativePath) address.PkgPath {
	if dir == "." {
		return v.ws.Module()
	}
	return address.PkgPath(string(v.ws.Module()) + "/" + string(dir))
}

// fsetOf is the FileSet a package's positions live in: the external
// cache's for dependencies, the workspace FileSet otherwise.
func (v *View) fsetOf(pkg *workspace.Package) *token.FileSet {
	return v.ws.FsetOf(pkg)
}
