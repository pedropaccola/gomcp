package gate

import (
	"fmt"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// MoveFile relocates a file to another package (newPkgPath) and/or gives
// it a new bare name (newName), any combination — at least one must be
// given. Moving into a different package is refused when it would break
// something Workspace.MoveConflicts can prove in advance — a method left
// without its receiver type, a name collision at the destination, the
// moved code depending on an unexported sibling staying behind, or code
// staying behind depending on an unexported declaration that's leaving.
// Otherwise every surviving reference across the move boundary has its
// qualifier fixed up first (Workspace.QualifierFixups) — external callers
// of the file's exported declarations, and the file's own outbound
// references to exported siblings staying behind, alike.
func (tx *Tx) MoveFile(pkg address.PkgPath, fileName string, newPkgPath address.PkgPath, newName string) error {
	if newPkgPath == "" && newName == "" {
		return fmt.Errorf("nothing to do for %q: give newPkgPath and/or newName", fileName)
	}
	unit, ok := tx.ws.Unit(pkg)
	if !ok {
		return fmt.Errorf("no package at %q", pkg)
	}
	for i, owner := range []*workspace.Package{unit.Prod(), unit.XTest()} {
		if owner == nil {
			continue
		}
		isXTest := i == 1
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
			tx.ws.MoveFile(pkg, isXTest, path, newPath)
			tx.touch(path, newPath)
			return nil
		}
		var movingKeys []string
		for _, sym := range owner.Symbols() {
			if sym.File == path {
				movingKeys = append(movingKeys, sym.Key())
			}
		}
		if conflicts := tx.ws.MoveConflicts(pkg, newPkgPath, movingKeys); len(conflicts) > 0 {
			return fmt.Errorf("moving %q to %q would break the workspace: %s", fileName, newPkgPath, strings.Join(conflicts, "; "))
		}
		fixups, ferr := tx.ws.QualifierFixups(movingKeys, pkg, newPkgPath)
		if ferr != nil {
			return ferr
		}
		if len(fixups) > 0 {
			if err := tx.applyFileSplices(toSplices(fixups)); err != nil {
				return err
			}
			// applyFileSplices may have forked owner's or destOwner's
			// package if either had a file among the fixups — re-resolve
			// both from their stable addresses rather than trust pointers
			// taken before the splice.
			if isXTest {
				owner, ok = tx.resolveXTest(pkg)
			} else {
				owner, ok = tx.resolvePackage(pkg)
			}
			if !ok {
				return fmt.Errorf("internal error: %q vanished after qualifier fixups", pkg)
			}
			destOwner, ok = tx.resolvePackage(newPkgPath)
			if !ok {
				return fmt.Errorf("internal error: %q vanished after qualifier fixups", newPkgPath)
			}
		}
		file, _ := owner.File(path)
		candidate := file.Src()
		if sp, ok := tx.offsetSpan(path, file.Ast().Name.Pos(), file.Ast().Name.End()); ok {
			candidate = applySplices(candidate, []splice{{span: sp, repl: []byte(destOwner.Name)}})
		}
		tx.ws.DropFile(pkg, isXTest, path)
		tx.touch(path)
		return tx.reloadFile(newPkgPath, false, newPath, candidate)
	}
	return fmt.Errorf("no file %q in %q", fileName, pkg)
}

// MovePackage moves a package to a new address, rewriting the import
// path in every importer. When the package name equals the old address
// base (the convention), the package clause, every unaliased qualifier,
// and each file's own "Package oldBase" doc-comment opening are renamed
// too; aliased imports keep their alias untouched.
func (tx *Tx) MovePackage(oldPkg, newPkg address.PkgPath) error {
	dir, ok := tx.dirOf(oldPkg)
	if !ok || dir == "." {
		return fmt.Errorf("no workspace package at %q", oldPkg)
	}
	newDir, ok := tx.dirOf(newPkg)
	if !ok || newDir == "." || newDir.IsOutsideRoot() {
		return fmt.Errorf("cannot move %q to %q: workspace packages live under module %q", oldPkg, newPkg, tx.ws.Module())
	}
	unit, ok := tx.ws.Unit(oldPkg)
	if !ok {
		return fmt.Errorf("no package at %q", oldPkg)
	}
	if _, exists := tx.ws.Unit(newPkg); exists {
		return fmt.Errorf("a package already exists at %q", newPkg)
	}
	oldBase, newBase := filepath.Base(string(dir)), filepath.Base(string(newDir))
	renameName := unit.Prod() != nil && unit.Prod().Name == oldBase && oldBase != newBase
	if renameName && !token.IsIdentifier(newBase) {
		return fmt.Errorf("%q is not a valid package name", newBase)
	}

	edits := tx.ws.PackageMoveSplices(oldPkg, newPkg, renameName, oldBase, newBase)
	if err := tx.applyFileSplices(toSplices(edits)); err != nil {
		return err
	}
	// applyFileSplices may have forked unit.XTest's package (it imports
	// its own Prod sibling, so it's a splice target) — re-resolve the
	// unit fresh rather than trust the pointers captured before the
	// splice.
	unit, ok = tx.ws.Unit(oldPkg)
	if !ok {
		return fmt.Errorf("internal error: %q vanished after import rewrites", oldPkg)
	}

	// Move the address's packages, renaming package clauses when due. Both
	// halves' shells are built before NewUnit assembles them atomically —
	// there is no point where a half-built Unit could be installed or
	// observed, since NewUnit is the only way to construct one at all.
	type half struct {
		orig, moved *workspace.Package
	}
	halves := [2]half{}
	for i, orig := range []*workspace.Package{unit.Prod(), unit.XTest()} {
		if orig == nil {
			continue
		}
		moved := orig.CloneShell()
		moved.Path = newDir
		moved.PkgPath = address.PkgPath(strings.Replace(string(orig.PkgPath), string(oldPkg), string(newPkg), 1))
		if renameName {
			moved.Name = newBase + strings.TrimPrefix(orig.Name, oldBase)
		}
		halves[i] = half{orig: orig, moved: moved}
	}
	// Every moved file re-enters through the content pipeline, so SwapFile
	// stays the one door for file content.
	tx.ws.InstallUnit(newPkg, workspace.NewUnit(halves[0].moved, halves[1].moved))
	for i, h := range halves {
		if h.orig == nil {
			continue
		}
		isXTest := i == 1
		for _, file := range h.orig.Files() {
			newPath := newDir.Join(filepath.Base(string(file.Path)))
			tx.ws.Tombstone(file.Path, h.orig.Name)
			tx.ws.ClearTombstone(newPath)
			tx.touch(file.Path, newPath)
			candidate := file.Src()
			if renameName {
				var fileSplices []splice
				if sp, ok := tx.offsetSpan(file.Path, file.Ast().Name.Pos(), file.Ast().Name.End()); ok {
					fileSplices = append(fileSplices, splice{span: sp, repl: []byte(h.moved.Name)})
				} else {
					return fmt.Errorf("cannot locate package clause of %q", file.Path)
				}
				if sp, ok := tx.leadingDocWord(file.Path, file.Ast().Doc, "Package ", oldBase); ok {
					fileSplices = append(fileSplices, splice{span: sp, repl: []byte(newBase)})
				}
				candidate = applySplices(file.Src(), fileSplices)
			}
			if err := tx.reloadFile(newPkg, isXTest, newPath, candidate); err != nil {
				return err
			}
		}
	}
	tx.ws.RemoveUnit(oldPkg)
	return nil
}

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
		newName, err := tx.ws.ValidateNewName(pkg, key, newSymbolKey)
		if err != nil {
			return err
		}
		sym, _, ok := tx.resolveSymbol(pkg, key)
		if !ok {
			return fmt.Errorf("no symbol %q in %q", key, pkg)
		}
		// Captured as plain values, not read off sym again after the
		// rename: renameSymbol calls applyFileSplices, which can fork the
		// package sym was resolved from, and a Kind/Recv pair extracted
		// now stays correct regardless — rename never changes either.
		isMethod, recv := sym.Kind == workspace.KindMethod, sym.Recv
		if err := tx.renameSymbol(pkg, key, newName); err != nil {
			return err
		}
		workingKey = newName
		if isMethod {
			workingKey = recv + "." + newName
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

// relocateSymbol is MoveSymbol's file-relocation half: extract key's
// declaration from srcPkg and splice it into a file of destPkg (destPkg
// equals srcPkg for a same-package move). destPkg must already exist.
// Cross-package relocation is refused when Workspace.MoveConflicts can
// prove in advance it would break the workspace; otherwise every
// surviving reference across the move boundary has its qualifier fixed up
// first (Workspace.QualifierFixups), so both the declaration's callers
// and the declaration's own outbound references keep resolving from
// their new vantage point. Private: composed by MoveSymbol, never called
// standalone.
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
	if conflicts := tx.ws.MoveConflicts(srcPkg, destPkg, []string{key}); len(conflicts) > 0 {
		return fmt.Errorf("moving %q to %q would break the workspace: %s", key, destPkg, strings.Join(conflicts, "; "))
	}
	srcIsXTest := isXTestOwner(tx.ws, srcPkg, owner)
	if srcPkg != destPkg {
		fixups, ferr := tx.ws.QualifierFixups([]string{key}, srcPkg, destPkg)
		if ferr != nil {
			return ferr
		}
		if len(fixups) > 0 {
			if err := tx.applyFileSplices(toSplices(fixups)); err != nil {
				return err
			}
			sym, owner, ok = tx.resolveSymbol(srcPkg, key)
			if !ok {
				return fmt.Errorf("internal error: %q vanished after qualifier fixups", key)
			}
			destOwner, ok = tx.resolvePackage(destPkg)
			if !ok {
				return fmt.Errorf("internal error: %q vanished after qualifier fixups", destPkg)
			}
			srcIsXTest = isXTestOwner(tx.ws, srcPkg, owner)
		}
	}
	src, extractSplice, err := tx.ws.ExtractDecl(srcPkg, key)
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
	file, _ := owner.File(sym.File)
	if err := tx.reloadFile(srcPkg, srcIsXTest, sym.File, applySplices(file.Src(), []splice{{span: span{start: extractSplice.Start, end: extractSplice.End}}})); err != nil {
		return err
	}
	if !inDest {
		return tx.reloadFile(destPkg, false, destPath, []byte("package "+destOwner.Name+"\n\n"+src+"\n"))
	}
	// reloadFile above may have forked destPkg's package when srcPkg ==
	// destPkg (a same-package relocation targets the same package the
	// source file just reloaded into) — re-resolve dest fresh rather than
	// trust the pointer captured before that reload.
	dest, _, ok = tx.resolveFile(destPath)
	if !ok {
		return fmt.Errorf("internal error: %q vanished after relocation", destPath)
	}
	at, ok := tx.ws.InsertOffset(destPkg, destPath, workspace.SymbolKind(frag.kind), frag.recv)
	if !ok {
		return fmt.Errorf("cannot locate insertion point in %q", destPath)
	}
	return tx.reloadFile(destPkg, false, destPath, applySplices(dest.Src(), []splice{{span: span{start: at, end: at}, repl: []byte("\n\n" + src + "\n")}}))
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

	edits := make(map[address.RelativePath][]splice)
	def := workspace.DefiningIdent(sym)
	if sp, ok := tx.offsetSpan(sym.File, def.Pos(), def.End()); ok {
		edits[sym.File] = append(edits[sym.File], splice{span: sp, repl: []byte(newName)})
	}
	if sp, ok := tx.leadingDocWord(sym.File, symbolDoc(sym), "", sym.Name); ok {
		edits[sym.File] = append(edits[sym.File], splice{span: sp, repl: []byte(newName)})
	}
	uses, err := tx.ws.RenameSplices(pkg, key, newName)
	if err != nil {
		return err
	}
	for path, splices := range toSplices(uses) {
		edits[path] = append(edits[path], splices...)
	}
	return tx.applyFileSplices(edits)
}
