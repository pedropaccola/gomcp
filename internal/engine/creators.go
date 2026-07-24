package engine

import (
	"fmt"
	"go/token"
	"path/filepath"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// CreatePackage creates a new package at a module-prefixed address with one
// file named after the package. name defaults to the address base. Fails if
// the address already holds a package; the directory is created at Flush.
func (tx *Tx) CreatePackage(pkg address.PkgPath, name string) error {
	dir, ok := tx.dirOf(pkg)
	if !ok || dir == "." || dir.EscapesRoot() {
		return fmt.Errorf("cannot create a package at %q: workspace packages live under module %q", pkg, tx.ws.Module())
	}
	if _, exists := tx.ws.Unit(pkg); exists {
		return fmt.Errorf("a package already exists at %q", pkg)
	}
	if name == "" {
		name = filepath.Base(string(dir))
	}
	if !token.IsIdentifier(name) {
		return fmt.Errorf("%q is not a valid package name", name)
	}
	tx.ws.InstallUnit(pkg, &workspace.Unit{Prod: workspace.NewPackage(name, dir, pkg, nil, nil, false)})
	return tx.reloadFile(pkg, false, dir.Join(name+".go"), []byte("package "+name+"\n"))
}

// CreateFile adds an empty file to an existing package, optionally seeded
// with a package doc comment.
func (tx *Tx) CreateFile(pkg address.PkgPath, name, doc string) error {
	p, ok := tx.resolvePackage(pkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", pkg)
	}
	path, err := fileAddress(p, name)
	if err != nil {
		return err
	}
	if _, _, exists := tx.resolveFile(path); exists {
		return fmt.Errorf("file %q already exists", path)
	}
	content := string(renderDocComment(doc)) + "package " + p.Name + "\n"
	return tx.reloadFile(pkg, false, path, []byte(content))
}

// CreateSymbol adds one new top-level declaration to a file of an existing
// package, at its canonical position. The file is required, never inferred —
// but a missing file inside the package is created implicitly, since
// creation cannot destroy. A new plain (non-position-dependent) const or
// var merges into the file's existing grouped block of the same kind, if
// one already exists — keeping placement decisions meaningful instead of
// proliferating interchangeable, unaddressable group shells; a new group
// is only created when none exists yet, and a standalone declaration is
// never retroactively converted into one. A new iota (position-dependent)
// group never merges — it always starts its own — and is placed next to
// its shared type's own declaration when typed and that type is in this
// file, the same clustering declPrecedes already gives methods with their
// receiver; otherwise it falls to the standard const/var region, same as
// an untyped iota group always does.
func (tx *Tx) CreateSymbol(pkg address.PkgPath, fileName, src string) error {
	p, ok := tx.resolvePackage(pkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", pkg)
	}
	frag, err := parseDeclFragment(src)
	if err != nil {
		return err
	}
	for _, key := range frag.keys {
		if key == "init" {
			continue // any number of init functions is legal
		}
		if _, exists := p.Symbol(key); exists {
			return fmt.Errorf("symbol %q already exists in %q: use EditSymbol", key, pkg)
		}
	}
	path, err := fileAddress(p, fileName)
	if err != nil {
		return err
	}
	file, ok := p.File(path)
	if !ok {
		candidate := []byte("package " + p.Name + "\n\n" + src + "\n")
		return tx.reloadFile(pkg, false, path, candidate)
	}

	if (frag.kind == KindConst || frag.kind == KindVar) && !frag.usesIota {
		tok := token.CONST
		if frag.kind == KindVar {
			tok = token.VAR
		}
		if existing := findMergeableGroup(file.Ast(), tok); existing != nil {
			specs, _, err := constVarSpecs(src)
			if err != nil {
				return err
			}
			at, ok := tx.offsetSpan(path, existing.Rparen, existing.Rparen)
			if !ok {
				return fmt.Errorf("cannot locate insertion point in %q", path)
			}
			return tx.reloadFile(pkg, false, path, applySplices(file.Src(), []splice{{span: span{start: at.start, end: at.start}, repl: []byte("\n" + specs + "\n")}}))
		}
	}

	at := tx.insertOffset(file, frag)
	if frag.kind == KindConst && frag.usesIota {
		if _, typeName, terr := constVarSpecs(src); terr == nil && typeName != "" {
			if anchor := tx.typeDeclOffset(file, typeName); anchor >= 0 {
				at = anchor
			}
		}
	}
	return tx.reloadFile(pkg, false, path, applySplices(file.Src(), []splice{{span: span{start: at, end: at}, repl: []byte("\n\n" + src + "\n")}}))
}
