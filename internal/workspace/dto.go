package workspace

import (
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
)

// newDTOSymbol copies a workspace symbol into the shared dto shape.
func newDTOSymbol(s *Symbol) dto.Symbol {
	return dto.NewSymbol(s.Key(), dto.SymbolKind(s.Kind), s.Recv, s.File)
}

// newDTOFile copies a workspace file's read-only facts into the shared
// dto shape.
func newDTOFile(f *File) dto.File {
	return dto.NewFile(f.Path, f.Doc())
}

// newDTOPackage copies a workspace package's read-only facts into the
// shared dto shape: its files and symbols, translated recursively. dir is
// the canonical address of the directory p occupies — the caller's job
// to resolve, since a bare *Package carries no back-reference to its
// owning Unit (see Unit.Dir).
func newDTOPackage(p *Package, dir address.PkgPath) dto.Package {
	wf := p.Files()
	files := make([]dto.File, len(wf))
	for i, f := range wf {
		files[i] = newDTOFile(f)
	}
	ws := p.Symbols()
	symbols := make([]dto.Symbol, len(ws))
	for i, s := range ws {
		symbols[i] = newDTOSymbol(s)
	}
	return dto.NewPackage(dir, p.PkgPath, files, symbols, p.Doc())
}

// Package resolves a canonical package address to its production package.
func (w *Workspace) Package(pkg address.PkgPath) (dto.Package, bool) {
	p, ok := w.ProdPackage(pkg)
	if !ok {
		return dto.Package{}, false
	}
	return newDTOPackage(p, p.PkgPath), true
}

// ExternalPackage resolves a dependency resident in the external cache;
// LoadExternal fills the cache outside the read gate.
func (w *Workspace) ExternalPackage(pkg address.PkgPath) (dto.Package, bool) {
	p, ok := w.LookupExternal(pkg)
	if !ok {
		return dto.Package{}, false
	}
	return newDTOPackage(p, ""), true
}

// Symbol resolves a package address and symbol key ("Name" or "Recv.Name")
// to the symbol and its owning package, checking Prod before XTest before
// falling back to the external dependency cache.
func (w *Workspace) Symbol(pkg address.PkgPath, key string) (dto.Symbol, dto.Package, bool) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return dto.Symbol{}, dto.Package{}, false
	}
	var dir address.PkgPath
	if unit, ok := w.Unit(pkg); ok {
		dir = unit.Dir()
	}
	return newDTOSymbol(sym), newDTOPackage(owner, dir), true
}

// Packages enumerates every package in the workspace: addresses in path
// order, Prod before XTest.
func (w *Workspace) Packages() []dto.Package {
	var out []dto.Package
	for _, addr := range w.UnitKeys() {
		unit, _ := w.Unit(addr)
		if prod := unit.Prod(); prod != nil {
			out = append(out, newDTOPackage(prod, unit.Dir()))
		}
		if xtest := unit.XTest(); xtest != nil {
			out = append(out, newDTOPackage(xtest, unit.Dir()))
		}
	}
	return out
}
