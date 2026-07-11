package engine

import "github.com/pedropaccola/gomcp/internal/engine/workspace"

// allPackages enumerates every package in the workspace, in the
// workspace's own model type: addresses in path order, Prod before XTest.
func (v *View) allPackages() []*workspace.Package {
	var out []*workspace.Package
	for _, pkg := range v.eng.ws.UnitKeys() {
		unit, _ := v.eng.ws.Unit(pkg)
		if unit.Prod != nil {
			out = append(out, unit.Prod)
		}
		if unit.XTest != nil {
			out = append(out, unit.XTest)
		}
	}
	return out
}

// Methods enumerates the methods declared on typeName in one package.
func (v *View) Methods(pkg Package, typeName string) []Symbol {
	var out []Symbol
	for _, sym := range pkg.Symbols() {
		if sym.Kind() == KindMethod && sym.Recv() == typeName {
			out = append(out, sym)
		}
	}
	return out
}

// Packages enumerates every package in the workspace: addresses in path
// order, Prod before XTest.
func (v *View) Packages() []Package {
	pkgs := v.allPackages()
	out := make([]Package, len(pkgs))
	for i, p := range pkgs {
		out[i] = newPackage(p)
	}
	return out
}
