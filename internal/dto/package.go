package dto

import (
	"github.com/pedropaccola/gomcp/internal/address"
)

// Package is dto's read-only view of one compiled package: its address,
// files, symbols, and godoc, constructed directly by workspace so it
// carries no pointer into the live model (no AST, no type-checker
// output).
type Package struct {
	Path    address.PkgPath
	Doc     string
	Files   []File
	Symbols []Symbol
}

// Symbol resolves one symbol by key ("Name" or "Recv.Name") within the
// package.
func (p Package) Symbol(key string) (Symbol, bool) {
	for _, s := range p.Symbols {
		if s.Key == key {
			return s, true
		}
	}
	return Symbol{}, false
}
