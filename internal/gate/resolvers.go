package gate

import (
	"go/token"
	"path/filepath"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// ExternalPackage resolves a dependency resident in the external cache;
// LoadExternal fills the cache outside the read gate.
func (v *View) ExternalPackage(pkg address.PkgPath) (dto.Package, bool) {
	return v.ws.ExternalPackage(pkg)
}

// Module is the workspace's module path: the prefix of every workspace
// package address. Valid inside Read, where the snapshot is held.
func (v *View) Module() address.PkgPath {
	return v.ws.Module()
}

// Package resolves a canonical package address to its production package.
func (v *View) Package(pkg address.PkgPath) (dto.Package, bool) {
	return v.ws.Package(pkg)
}

// Symbol resolves a package address and symbol key ("Name" or "Recv.Name")
// to the symbol and its owning package, checking Prod before XTest.
func (v *View) Symbol(pkg address.PkgPath, key string) (dto.Symbol, dto.Package, bool) {
	return v.ws.Symbol(pkg, key)
}

// fsetOf is the FileSet a package's positions live in: the external
// cache's for dependencies, the workspace FileSet otherwise.
func (v *View) fsetOf(pkg *workspace.Package) *token.FileSet {
	return v.ws.FsetOf(pkg)
}

// resolveFileByPath resolves a file path to the file and its owning package, in
// the workspace's own model types, checking the production package before
// the external test package. Dependency files resolve through their
// import-path-qualified pseudo-paths. path's own directory is its owning
// package's canonical PkgPath by construction (every FilePath is built as
// pkg+"/"+basename — see address.PkgPath.File) — no module-prefixing
// derivation needed here.
func (v *View) resolveFileByPath(path address.FilePath) (*workspace.File, *workspace.Package, bool) {
	pkgPath := address.PkgPath(filepath.Dir(string(path)))
	if unit, ok := v.ws.Unit(pkgPath); ok {
		for _, pkg := range unit.Members() {
			if file, ok := pkg.File(path); ok {
				return file, pkg, true
			}
		}
	}
	if pkg, ok := v.ws.LookupExternal(pkgPath); ok {
		if file, ok := pkg.File(path); ok {
			return file, pkg, true
		}
	}
	return nil, nil, false
}

// resolveXTest resolves a canonical package address to its external test
// package, in the workspace's own model type.
func (v *View) resolveXTest(pkg address.PkgPath) (*workspace.Package, bool) {
	unit, ok := v.ws.Unit(pkg)
	if !ok {
		return nil, false
	}
	xtest := unit.XTest()
	if xtest == nil {
		return nil, false
	}
	return xtest, true
}
