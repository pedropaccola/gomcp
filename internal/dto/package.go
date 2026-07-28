package dto

import (
	"github.com/pedropaccola/gomcp/internal/address"
)

// Package is dto's read-only view of one compiled package: its address,
// files, symbols, and godoc, constructed directly by workspace so it
// carries no pointer into the live model (no AST, no type-checker
// output).
type Package struct {
	dir     address.PkgPath
	pkgPath address.PkgPath
	files   []File
	symbols []Symbol
	doc     string
}

// Doc is the package's godoc — every file's own doc comment, concatenated
// in file order.
func (p Package) Doc() string { return p.doc }

// Files enumerates the package's files, sorted by path.
func (p Package) Files() []File { return p.files }

// Dir is the canonical address of the directory the package occupies
// (the zero value for a dependency, which has none).
func (p Package) Dir() address.PkgPath { return p.dir }

// PkgPath is the package's import path: its canonical address.
func (p Package) PkgPath() address.PkgPath { return p.pkgPath }

// Symbol resolves one symbol by key ("Name" or "Recv.Name") within the
// package.
func (p Package) Symbol(key string) (Symbol, bool) {
	for _, s := range p.symbols {
		if s.key == key {
			return s, true
		}
	}
	return Symbol{}, false
}

// Symbols enumerates the package's symbols, sorted by key.
func (p Package) Symbols() []Symbol { return p.symbols }

// NewPackage constructs a Package from its plain fields.
func NewPackage(dir, pkgPath address.PkgPath, files []File, symbols []Symbol, doc string) Package {
	return Package{dir: dir, pkgPath: pkgPath, files: files, symbols: symbols, doc: doc}
}
