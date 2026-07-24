package workspace

import (
	"go/token"

	"github.com/pedropaccola/gomcp/internal/address"
)

// ExternalPackage resolves a dependency resident in the cache.
func (w *Workspace) ExternalPackage(pkg address.PkgPath) (*Package, bool) {
	p, ok := w.external[pkg]
	return p, ok
}

// InstallExternal caches a loaded dependency.
func (w *Workspace) InstallExternal(pkg address.PkgPath, p *Package) {
	w.external[pkg] = p
}

// ExternalFailure reports a dependency address's cached load error.
func (w *Workspace) ExternalFailure(pkg address.PkgPath) (error, bool) {
	err, ok := w.externalErr[pkg]
	return err, ok
}

// FailExternal caches a dependency load failure, so a mistyped address
// costs one load per session.
func (w *Workspace) FailExternal(pkg address.PkgPath, err error) {
	w.externalErr[pkg] = err
}

// ExternalFset is the dependency cache's own position table: workspace
// swaps never invalidate cached positions.
func (w *Workspace) ExternalFset() *token.FileSet {
	return w.externalFset
}
