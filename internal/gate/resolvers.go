package gate

import (
	"fmt"
	"go/token"

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

// File resolves a bare filename against an already-resolved package's own
// files.
func (v *View) File(pkg dto.Package, fileName string) (dto.File, error) {
	fp, err := address.NewFilePath(v.Module(), pkg.Path, fileName)
	if err != nil {
		return dto.File{}, err
	}
	if file, ok := pkg.File(fp); ok {
		return file, nil
	}
	return dto.File{}, fmt.Errorf("no file %q in package %q: call list_files for valid names", fp.Base(), pkg.Path.Base())
}
