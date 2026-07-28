package gate

import (
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
)

// Methods enumerates the methods declared on typeName in one package.
func (v *View) Methods(pkg dto.Package, typeName string) []dto.Symbol {
	var out []dto.Symbol
	for _, sym := range pkg.Symbols {
		if sym.Kind == dto.KindMethod && sym.Recv == typeName {
			out = append(out, sym)
		}
	}
	return out
}

// Packages enumerates every package in the workspace: addresses in path
// order, Prod before XTest.
func (v *View) Packages() []dto.Package {
	return v.ws.Packages()
}

// UnitKeys enumerates every unit's address, sorted — one entry per
// directory, unlike Packages which emits Prod and XTest separately.
func (v *View) UnitKeys() []address.PkgPath {
	return v.ws.UnitKeys()
}
