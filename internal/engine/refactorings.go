package engine

import (
	"fmt"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine/workspace"
)

// MoveSymbol relocates key to another file — the same package's file when
// newPkgPath is empty, a different package's file otherwise — and/or
// renames it, any combination; at least one of newPkgPath (with
// newFileName), newFileName, or newSymbolKey must be given. newSymbolKey
// follows the same grammar as any symbol address: a bare identifier for a
// non-method, "Recv.Name" for a method — and for a method it is required
// to be qualified, Recv must match the symbol's actual receiver exactly,
// since a rename can never change what a method belongs to. A rename is
// applied first (workspace-wide reference chasing, exactly as a standalone
// rename would), then the — possibly renamed — declaration is relocated.
// Renaming a member of an iota group is safe and always allowed — a
// rename never touches position or order, only relocation can. Relocation
// itself refuses a member whose meaning depends on its position in the
// group (iota, inherited const values): a single member can't be
// extracted alone without breaking the group's remaining positions.
// Cross-package relocation does not rewrite qualifiers at use sites still
// referring to the old package — that surfaces as ordinary diagnostics
// afterward, the same way any other edit's collateral damage does. Moves
// never cross the test build boundary, and the destination file is
// created when missing.
func (tx *Tx) MoveSymbol(pkg address.PkgPath, key string, newPkgPath address.PkgPath, newFileName, newSymbolKey string) error {
	if newPkgPath != "" && newFileName == "" {
		return fmt.Errorf("newPkgPath given without newFileName: a cross-package move must name the destination file")
	}
	if newPkgPath == "" && newFileName == "" && newSymbolKey == "" {
		return fmt.Errorf("nothing to do for %q: give newPkgPath (with newFileName), newFileName, and/or newSymbolKey", key)
	}
	workingKey := key
	if newSymbolKey != "" {
		sym, _, ok := tx.resolveSymbol(pkg, key)
		if !ok {
			return fmt.Errorf("no symbol %q in %q", key, pkg)
		}
		newName, err := splitNewSymbolKey(sym, newSymbolKey)
		if err != nil {
			return err
		}
		if err := tx.renameSymbol(pkg, key, newName); err != nil {
			return err
		}
		workingKey = newName
		if sym.Kind == workspace.KindMethod {
			workingKey = sym.Recv + "." + newName
		}
	}
	if newFileName == "" {
		return nil
	}
	destPkg := pkg
	if newPkgPath != "" {
		destPkg = newPkgPath
	}
	return tx.relocateSymbol(pkg, destPkg, workingKey, newFileName)
}

// renameSymbol renames key to newName everywhere: the defining identifier,
// its own leading doc-comment mention (when the doc follows Go's
// name-first convention), and every resolved use across the workspace,
// matched by qualified name. Renames exactly this one object — renaming an
// interface method does not chase implementors; broken satisfactions
// arrive in the echo instead. Private: the public verb is MoveSymbol,
// which composes this with relocateSymbol.
func (tx *Tx) renameSymbol(pkg address.PkgPath, key, newName string) error {
	if !token.IsIdentifier(newName) {
		return fmt.Errorf("%q is not a valid identifier", newName)
	}
	sym, owner, ok := tx.resolveSymbol(pkg, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, pkg)
	}
	newKey := newName
	if sym.Kind == workspace.KindMethod {
		newKey = sym.Recv + "." + newName
	}
	if _, exists := owner.Symbol(newKey); exists {
		return fmt.Errorf("symbol %q already exists in %q", newKey, pkg)
	}
	target := objKey(tx.objectOf(sym))
	if target == "" {
		return fmt.Errorf("type information unavailable for %q", key)
	}

	edits := make(map[address.RelativePath][]splice)
	def := definingIdent(sym)
	if sp, ok := tx.offsetSpan(sym.File, def.Pos(), def.End()); ok {
		edits[sym.File] = append(edits[sym.File], splice{span: sp, repl: []byte(newName)})
	}
	if sp, ok := tx.leadingDocWord(sym.File, symbolDoc(sym), "", sym.Name); ok {
		edits[sym.File] = append(edits[sym.File], splice{span: sp, repl: []byte(newName)})
	}
	tx.gatherUses(target, func(relFile address.RelativePath, sp span) {
		edits[relFile] = append(edits[relFile], splice{span: sp, repl: []byte(newName)})
	})
	return tx.applyFileSplices(edits)
}

// MoveFile relocates a file to another package (newPkgPath) and/or gives
// it a new bare name (newName), any combination — at least one must be
// given. Moving into a different package can leave declarations that
// referenced now-out-of-scope unexported siblings broken; that surfaces
// as ordinary diagnostics afterward, not a refusal — the same way any
// other edit's collateral damage does.
func (tx *Tx) MoveFile(pkg address.PkgPath, fileName string, newPkgPath address.PkgPath, newName string) error {
	if newPkgPath == "" && newName == "" {
		return fmt.Errorf("nothing to do for %q: give newPkgPath and/or newName", fileName)
	}
	unit, ok := tx.eng.ws.Unit(pkg)
	if !ok {
		return fmt.Errorf("no package at %q", pkg)
	}
	for _, owner := range []*workspace.Package{unit.Prod, unit.XTest} {
		if owner == nil {
			continue
		}
		path, err := fileAddress(owner, fileName)
		if err != nil {
			return err
		}
		if _, ok := owner.File(path); !ok {
			continue
		}
		destOwner := owner
		if newPkgPath != "" {
			destOwner, ok = tx.resolvePackage(newPkgPath)
			if !ok {
				return fmt.Errorf("no package at %q: create_package first", newPkgPath)
			}
		}
		baseName := fileName
		if newName != "" {
			baseName = newName
		}
		newPath, err := fileAddress(destOwner, baseName)
		if err != nil {
			return err
		}
		if _, _, exists := tx.resolveFile(newPath); exists {
			return fmt.Errorf("file %q already exists", newPath)
		}
		if destOwner == owner {
			tx.eng.ws.MoveFile(owner, path, newPath)
			tx.touch(path, newPath)
			return nil
		}
		file, _ := owner.File(path)
		candidate := file.Src()
		if sp, ok := tx.offsetSpan(path, file.Ast().Name.Pos(), file.Ast().Name.End()); ok {
			candidate = applySplices(candidate, []splice{{span: sp, repl: []byte(destOwner.Name)}})
		}
		tx.eng.ws.DropFile(pkg, owner, path)
		tx.touch(path)
		return tx.reloadFile(destOwner, newPath, candidate)
	}
	return fmt.Errorf("no file %q in %q", fileName, pkg)
}

// MovePackage moves a package to a new address, rewriting the import
// path in every importer. When the package name equals the old address
// base (the convention), the package clause, every unaliased qualifier,
// and each file's own "Package oldBase" doc-comment opening are renamed
// too; aliased imports keep their alias untouched.
func (tx *Tx) MovePackage(oldPkg, newPkg address.PkgPath) error {
	dir, ok := tx.eng.dirOf(oldPkg)
	if !ok || dir == "." {
		return fmt.Errorf("no workspace package at %q", oldPkg)
	}
	newDir, ok := tx.eng.dirOf(newPkg)
	if !ok || newDir == "." || newDir.EscapesRoot() {
		return fmt.Errorf("cannot move %q to %q: workspace packages live under module %q", oldPkg, newPkg, tx.eng.ws.Module())
	}
	unit, ok := tx.eng.ws.Unit(oldPkg)
	if !ok {
		return fmt.Errorf("no package at %q", oldPkg)
	}
	if _, exists := tx.eng.ws.Unit(newPkg); exists {
		return fmt.Errorf("a package already exists at %q", newPkg)
	}
	oldBase, newBase := filepath.Base(string(dir)), filepath.Base(string(newDir))
	renameName := unit.Prod != nil && unit.Prod.Name == oldBase && oldBase != newBase
	if renameName && !token.IsIdentifier(newBase) {
		return fmt.Errorf("%q is not a valid package name", newBase)
	}
	oldImport, newImport := string(oldPkg), string(newPkg)

	// Importers first: their files are disjoint from the moving package's.
	// The unit's own XTest package is an importer too — it imports its
	// production sibling — so only Prod itself is skipped.
	edits := make(map[address.RelativePath][]splice)
	for _, pkg := range tx.allPackages() {
		if pkg == unit.Prod {
			continue
		}
		for _, file := range pkg.Files() {
			for _, imp := range file.Ast().Imports {
				if imp.Path.Value != strconv.Quote(oldImport) {
					continue
				}
				if sp, ok := tx.offsetSpan(file.Path, imp.Path.Pos(), imp.Path.End()); ok {
					edits[file.Path] = append(edits[file.Path], splice{span: sp, repl: []byte(strconv.Quote(newImport))})
				}
			}
		}
		if renameName && pkg.TypesInfo() != nil {
			for ident, obj := range pkg.TypesInfo().Uses {
				pkgName, ok := obj.(*types.PkgName)
				if !ok || pkgName.Imported() == nil || pkgName.Imported().Path() != oldImport {
					continue
				}
				if ident.Name != oldBase {
					continue // aliased import: the alias survives the move
				}
				relFile, err := tx.eng.relativePath(tx.eng.ws.FileSet().Position(ident.Pos()).Filename)
				if err != nil || relFile.EscapesRoot() {
					continue
				}
				if sp, ok := tx.offsetSpan(relFile, ident.Pos(), ident.End()); ok {
					edits[relFile] = append(edits[relFile], splice{span: sp, repl: []byte(newBase)})
				}
			}
		}
	}
	if err := tx.applyFileSplices(edits); err != nil {
		return err
	}

	// Move the address's packages, renaming package clauses when due.
	// Every moved file re-enters through the content pipeline, so SwapFile
	// stays the one door for file content.
	newUnit := &workspace.Unit{}
	for i, pkg := range []*workspace.Package{unit.Prod, unit.XTest} {
		if pkg == nil {
			continue
		}
		moved := pkg.CloneShell()
		moved.Path = newDir
		moved.PkgPath = address.PkgPath(strings.Replace(string(pkg.PkgPath), oldImport, newImport, 1))
		if renameName {
			moved.Name = newBase + strings.TrimPrefix(pkg.Name, oldBase)
		}
		for _, file := range pkg.Files() {
			newPath := newDir.Join(filepath.Base(string(file.Path)))
			tx.eng.ws.Tombstone(file.Path, pkg.Name)
			tx.eng.ws.ClearTombstone(newPath)
			tx.touch(file.Path, newPath)
			candidate := file.Src()
			if renameName {
				var fileSplices []splice
				if sp, ok := tx.offsetSpan(file.Path, file.Ast().Name.Pos(), file.Ast().Name.End()); ok {
					fileSplices = append(fileSplices, splice{span: sp, repl: []byte(moved.Name)})
				} else {
					return fmt.Errorf("cannot locate package clause of %q", file.Path)
				}
				if sp, ok := tx.leadingDocWord(file.Path, file.Ast().Doc, "Package ", oldBase); ok {
					fileSplices = append(fileSplices, splice{span: sp, repl: []byte(newBase)})
				}
				candidate = applySplices(file.Src(), fileSplices)
			}
			if err := tx.reloadFile(moved, newPath, candidate); err != nil {
				return err
			}
		}
		if i == 0 {
			newUnit.Prod = moved
		} else {
			newUnit.XTest = moved
		}
	}
	tx.eng.ws.RemoveUnit(oldPkg)
	tx.eng.ws.InstallUnit(newPkg, newUnit)
	return nil
}

// relocateSymbol is MoveSymbol's file-relocation half: extract key's
// declaration from srcPkg and splice it into a file of destPkg (destPkg
// equals srcPkg for a same-package move). destPkg must already exist.
// Private: composed by MoveSymbol, never called standalone.
func (tx *Tx) relocateSymbol(srcPkg, destPkg address.PkgPath, key, fileName string) error {
	sym, owner, ok := tx.resolveSymbol(srcPkg, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, srcPkg)
	}
	destOwner, ok := tx.resolvePackage(destPkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", destPkg)
	}
	destPath, err := fileAddress(destOwner, fileName)
	if err != nil {
		return err
	}
	if destOwner == owner && destPath == sym.File {
		return fmt.Errorf("%q already lives in %q", key, destPath)
	}
	if strings.HasSuffix(fileName, "_test.go") != strings.HasSuffix(sym.File.String(), "_test.go") {
		return fmt.Errorf("moving %q from %q to %q would cross the test build boundary", key, sym.File, destPath)
	}
	file, _ := owner.File(sym.File)
	src, sp, err := tx.extractDecl(sym, file)
	if err != nil {
		return err
	}
	frag, err := parseDeclFragment(src)
	if err != nil {
		return err
	}
	dest, inDest := destOwner.File(destPath)
	if _, _, exists := tx.resolveFile(destPath); exists && !inDest {
		return fmt.Errorf("file %q belongs to another package", destPath)
	}
	if err := tx.reloadFile(owner, sym.File, applySplices(file.Src(), []splice{{span: sp}})); err != nil {
		return err
	}
	if !inDest {
		return tx.reloadFile(destOwner, destPath, []byte("package "+destOwner.Name+"\n\n"+src+"\n"))
	}
	at := tx.insertOffset(dest, frag)
	return tx.reloadFile(destOwner, destPath, applySplices(dest.Src(), []splice{{span: span{start: at, end: at}, repl: []byte("\n\n" + src + "\n")}}))
}

// splitNewSymbolKey validates a MoveSymbol destination key against the
// symbol actually being renamed: a method's receiver can never change
// through a rename, so newKey must name it explicitly ("Recv.Name") and
// Recv must match; a non-method's newKey must be a bare identifier, since
// there is no receiver to qualify it with.
func splitNewSymbolKey(sym *workspace.Symbol, newKey string) (newName string, err error) {
	if sym.Kind != workspace.KindMethod {
		if strings.Contains(newKey, ".") {
			return "", fmt.Errorf("%q is not a method: newSymbolKey must be a bare identifier", sym.Key())
		}
		return newKey, nil
	}
	recv, name, ok := strings.Cut(newKey, ".")
	if !ok {
		return "", fmt.Errorf("%q is a method: newSymbolKey must be %q (its receiver cannot change)", sym.Key(), sym.Recv+".<new name>")
	}
	if recv != sym.Recv {
		return "", fmt.Errorf("cannot change %q's receiver via move_symbol: got %q, want %q", sym.Key(), recv, sym.Recv)
	}
	return name, nil
}
