package gate

import (
	"fmt"
	"go/token"
	"path/filepath"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// CreateFile adds an empty file to an existing package, optionally seeded
// with a package doc comment.
func (tx *Tx) CreateFile(pkg address.PkgPath, name, doc string) error {
	p, ok := tx.resolvePackage(pkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", pkg)
	}
	path, err := address.NewFilePath(tx.ws.Module(), p.PkgPath, name)
	if err != nil {
		return err
	}
	if _, _, exists := tx.resolveFileByPath(path); exists {
		return fmt.Errorf("file %q already exists", path)
	}
	content := string(renderDocComment(doc)) + "package " + p.Name + "\n"
	return tx.installFile(pkg, false, path, []byte(content))
}

// CreatePackage creates a new package at a module-prefixed address with one
// file named after the package. name defaults to the address base. Fails if
// the address already holds a package; the directory is created at Flush.
func (tx *Tx) CreatePackage(pkg address.PkgPath, name string) error {
	dir, ok := tx.dirOf(pkg)
	if !ok || dir == "." || address.IsOutsideRoot(dir) {
		return fmt.Errorf("cannot create a package at %q: workspace packages live under module %q", pkg, tx.ws.Module())
	}
	if _, exists := tx.ws.Unit(pkg); exists {
		return fmt.Errorf("a package already exists at %q", pkg)
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	if !token.IsIdentifier(name) {
		return fmt.Errorf("%q is not a valid package name", name)
	}
	tx.ws.InstallUnit(pkg, workspace.NewUnit(workspace.NewPackage(name, pkg, nil, nil, false), nil))
	return tx.installFile(pkg, false, pkg.File(name+".go"), []byte("package "+name+"\n"))
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
	path, err := address.NewFilePath(tx.ws.Module(), p.PkgPath, fileName)
	if err != nil {
		return err
	}
	file, ok := p.File(path)
	if !ok {
		candidate := []byte("package " + p.Name + "\n\n" + src + "\n")
		return tx.installFile(pkg, false, path, candidate)
	}

	if (frag.kind == dto.KindConst || frag.kind == dto.KindVar) && !frag.usesIota {
		tok := token.CONST
		if frag.kind == dto.KindVar {
			tok = token.VAR
		}
		if at, ok := tx.ws.MergeableGroupInsertOffset(pkg, path, tok); ok {
			specs, _, err := constVarEntries(src)
			if err != nil {
				return err
			}
			sp, ok := tx.ws.NewSpliceAtOffset(p, path, at, at, []byte("\n"+specs+"\n"))
			if !ok {
				return fmt.Errorf("cannot locate insertion point in %q", path)
			}
			return tx.installFile(pkg, false, path, workspace.ApplySplices(file.Src(), []workspace.Splice{sp}))
		}
	}

	at, ok := tx.ws.InsertOffset(pkg, path, workspace.SymbolKind(frag.kind), frag.recv)
	if !ok {
		return fmt.Errorf("cannot locate insertion point in %q", path)
	}
	if frag.kind == dto.KindConst && frag.usesIota {
		if _, typeName, terr := constVarEntries(src); terr == nil && typeName != "" {
			if anchor, ok := tx.ws.TypeDeclOffset(pkg, path, typeName); ok {
				at = anchor
			}
		}
	}
	sp, ok := tx.ws.NewSpliceAtOffset(p, path, at, at, []byte("\n\n"+src+"\n"))
	if !ok {
		return fmt.Errorf("cannot locate insertion point in %q", path)
	}
	return tx.installFile(pkg, false, path, workspace.ApplySplices(file.Src(), []workspace.Splice{sp}))
}
