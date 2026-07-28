package workspace

import (
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
)

// newDTOSymbol copies a workspace symbol into the shared dto shape.
func newDTOSymbol(s *Symbol) dto.Symbol {
	return dto.Symbol{Key: s.Key(), Kind: dto.SymbolKind(s.Kind), Recv: s.Recv, File: s.File}
}

// newDTOFile copies a workspace file's read-only facts into the shared
// dto shape.
func newDTOFile(f *File) dto.File {
	return dto.File{Path: f.Path, Doc: f.Doc()}
}

// newDTOPackage copies a workspace package's read-only facts into the
// shared dto shape: its files and symbols, translated recursively.
func newDTOPackage(p *Package) dto.Package {
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
	return dto.Package{Path: p.PkgPath, Doc: p.Doc(), Files: files, Symbols: symbols}
}

// Package resolves a canonical package address to its production package.
func (w *Workspace) Package(pkg address.PkgPath) (dto.Package, bool) {
	p, ok := w.ProdPackage(pkg)
	if !ok {
		return dto.Package{}, false
	}
	return newDTOPackage(p), true
}

// ExternalPackage resolves a dependency resident in the external cache;
// LoadExternal fills the cache outside a Read call.
func (w *Workspace) ExternalPackage(pkg address.PkgPath) (dto.Package, bool) {
	p, ok := w.LookupExternal(pkg)
	if !ok {
		return dto.Package{}, false
	}
	return newDTOPackage(p), true
}

// Symbol resolves a package address and symbol key ("Name" or "Recv.Name")
// to the symbol and its owning package, checking Prod before XTest before
// falling back to the external dependency cache.
func (w *Workspace) Symbol(pkg address.PkgPath, key string) (dto.Symbol, dto.Package, bool) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return dto.Symbol{}, dto.Package{}, false
	}
	return newDTOSymbol(sym), newDTOPackage(owner), true
}

// Packages enumerates every package in the workspace: addresses in path
// order, Prod before XTest.
func (w *Workspace) Packages() []dto.Package {
	var out []dto.Package
	for _, addr := range w.UnitKeys() {
		unit, _ := w.Unit(addr)
		if prod := unit.Prod(); prod != nil {
			out = append(out, newDTOPackage(prod))
		}
		if xtest := unit.XTest(); xtest != nil {
			out = append(out, newDTOPackage(xtest))
		}
	}
	return out
}
