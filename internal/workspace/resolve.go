package workspace

// ResolvePackage resolves id to the specific package it names — Prod or
// XTest, per id.Kind().
func (w *Workspace) ResolvePackage(id PackageID) (*Package, bool) {
	p := w.packageAt(id.Base(), id.Kind())
	return p, p != nil
}

// packageAt resolves addr's package for a specific kind — the shared
// lookup ResolvePackage and RelocateFile both compose on.
func (w *Workspace) packageAt(addr PackagePath, kind PackageKind) *Package {
	switch kind {
	case KindXTest:
		return w.xtest[addr]
	default:
		return w.prod[addr]
	}
}

// candidatePackages returns every package address for pkg, in resolution
// order: workspace members first (Prod, then XTest), the external
// dependency cache last as the final fallback. The one place this order
// is written — ResolveSymbol, ResolveSymbolIn, and any future resolver
// needing the same "workspace, then dependency" fallback compose on this
// instead of each re-deriving it by hand, which is what let
// ResolveSymbolIn ship without the external half in the first place.
func (w *Workspace) candidatePackages(pkg PackagePath) []*Package {
	members := w.MembersOf(pkg)
	if p, ok := w.LookupExternal(pkg); ok {
		return append(members, p)
	}
	return members
}
