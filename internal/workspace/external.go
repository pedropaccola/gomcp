package workspace

import (
	"go/token"
)

// LookupExternal resolves a dependency resident in the cache.
func (w *Workspace) LookupExternal(pkg PackagePath) (*Package, bool) {
	p, ok := w.external[pkg]
	return p, ok
}

// InstallExternal caches a loaded dependency.
func (w *Workspace) InstallExternal(pkg PackagePath, p *Package) {
	w.external[pkg] = p
}

// ExternalFailure reports a dependency address's cached load error.
func (w *Workspace) ExternalFailure(pkg PackagePath) (error, bool) {
	err, ok := w.externalErr[pkg]
	return err, ok
}

// FailExternal caches a dependency load failure, so a mistyped address
// costs one load per session.
func (w *Workspace) FailExternal(pkg PackagePath, err error) {
	w.externalErr[pkg] = err
}

// ExternalFset is the dependency cache's own position table: workspace
// swaps never invalidate cached positions.
func (w *Workspace) ExternalFset() *token.FileSet {
	return w.externalFset
}
