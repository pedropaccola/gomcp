package store

import (
	"fmt"
	"go/token"
	"strings"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// CreateFile adds an empty file to an existing package, optionally seeded
// with a package doc comment. pkg may name a unit's XTest half via its
// own "_test"-suffixed address (workspace.Workspace.EnsureXTest),
// installing that half — and a fresh Prod sibling too, if the whole
// package is new — the first time something targets it.
func (tx *Tx) CreateFile(pkg workspace.PackageID, name, doc string) error {
	p, freshProd, err := tx.ws.EnsureXTest(pkg)
	if err != nil {
		return err
	}
	if freshProd != nil {
		stub := freshProd.ID.Base().File(freshProd.Name + ".go")
		if err := tx.ws.SwapFile(freshProd.ID.Base(), false, stub, []byte("package "+freshProd.Name+"\n")); err != nil {
			return err
		}
		tx.markChanged(stub)
	}
	path, err := workspace.NewFilePath(tx.ws.Module(), p.ID.Base(), name)
	if err != nil {
		return err
	}
	if _, _, exists := tx.ws.ResolveFileByPath(path); exists {
		return fmt.Errorf("file %q already exists", path)
	}
	content := string(workspace.RenderDocComment(doc)) + "package " + p.Name + "\n"
	if err := tx.ws.SwapFile(p.ID.Base(), p.ID.Kind() == workspace.KindXTest, path, []byte(content)); err != nil {
		return err
	}
	tx.markChanged(path)
	return nil
}

// CreatePackage creates a new package at a module-prefixed address with one
// file named after the package. name defaults to the address base. Fails if
// the address already holds a package; the directory is created at Flush.
func (tx *Tx) CreatePackage(pkg workspace.PackagePath, name string) error {
	path, err := tx.ws.CreatePackage(pkg, name)
	if err != nil {
		return err
	}
	tx.markChanged(path)
	return nil
}

// CreateSymbol adds one new top-level declaration to a file of an existing
// package, at its canonical position. See Workspace.CreateSymbol for the
// full placement policy.
func (tx *Tx) CreateSymbol(pkg workspace.PackageID, fileName, src string) error {
	touched, err := tx.ws.CreateSymbol(pkg, fileName, src)
	if err != nil {
		return err
	}
	tx.markChanged(touched...)
	return nil
}

// DeleteFile removes one file and every declaration in it, tombstoning the
// path for Flush. Deletion is idempotent: a missing package or file is a
// noop, not a failure — the file being gone is the success condition,
// whoever caused it.
func (tx *Tx) DeleteFile(pkg workspace.PackagePath, name string) error {
	unit, ok := tx.ws.Unit(pkg)
	if !ok {
		return nil
	}
	for _, owner := range unit.Members() {
		path, err := workspace.NewFilePath(tx.ws.Module(), owner.ID.Base(), name)
		if err != nil {
			return err
		}
		if _, ok := owner.File(path); !ok {
			continue
		}
		tx.ws.DropFile(pkg, owner.ID.Kind() == workspace.KindXTest, path)
		tx.markChanged(path)
		return nil
	}
	return nil
}

// DeletePackage removes a whole package address, tombstoning every file.
// Deletion is idempotent: a missing package is a noop, not a failure.
func (tx *Tx) DeletePackage(pkg workspace.PackagePath) error {
	tx.markChanged(tx.ws.DropPackage(pkg)...)
	return nil
}

// DeleteSymbol removes key's declaration. Idempotent: a missing symbol is
// a noop, not a failure. See Workspace.DeleteSymbol for the full removal
// policy.
func (tx *Tx) DeleteSymbol(pkg workspace.PackagePath, key string) error {
	path, found, err := tx.ws.DeleteSymbol(pkg, key)
	if err != nil {
		return err
	}
	if found {
		tx.markChanged(path)
	}
	return nil
}

// EditFile replaces or clears a file's package doc comment — the comment
// block directly above the package clause — leaving the rest of the file
// untouched. The one sanctioned door into floating-comment space: every
// other comment stays unaddressable by design.
func (tx *Tx) EditFile(pkg workspace.PackagePath, name, doc string) error {
	p, ok := tx.ws.ProdPackage(pkg)
	if !ok {
		return workspace.NoPackageError(pkg)
	}
	path, err := workspace.NewFilePath(tx.ws.Module(), p.ID.Base(), name)
	if err != nil {
		return err
	}
	file, _, ok := tx.ws.ResolveFileByPath(path)
	if !ok {
		return errNoFile(name, pkg)
	}
	astFile := file.Ast()
	docPos, docEnd := astFile.Package, astFile.Package
	if astFile.Doc != nil {
		docPos = astFile.Doc.Pos()
	}
	sp, ok := tx.ws.NewSpliceAtPos(p, path, docPos, docEnd, workspace.RenderDocComment(doc))
	if !ok {
		return fmt.Errorf("cannot locate doc comment span in %q", path)
	}
	candidate := workspace.ByteSplices{sp}.Apply(file.Src())
	if err := tx.ws.SwapFile(pkg, false, path, candidate); err != nil {
		return err
	}
	tx.markChanged(path)
	return nil
}

// EditSymbol replaces key's whole declaration with src. See
// Workspace.EditSymbol for the full replacement policy.
func (tx *Tx) EditSymbol(pkg workspace.PackagePath, key, src string) error {
	path, err := tx.ws.EditSymbol(pkg, key, src)
	if err != nil {
		return err
	}
	tx.markChanged(path)
	return nil
}

// MoveFile relocates a file to another package (newPkgPath) and/or gives
// it a new bare name (newName), any combination — at least one must be
// given. Moving into a different package is refused when it would break
// something Workspace.DetectMoveConflicts can prove in advance — a method
// left without its receiver type, a name collision at the destination,
// the moved code depending on an unexported sibling staying behind, or
// code staying behind depending on an unexported declaration that's
// leaving. Otherwise every surviving reference across the move boundary
// has its qualifier fixed up first (Workspace.ComputeQualifierFixups) —
// external callers of the file's exported declarations, and the file's
// own outbound references to exported siblings staying behind, alike.
func (tx *Tx) MoveFile(pkg workspace.PackagePath, fileName string, newPkgPath workspace.PackagePath, newName string) error {
	if newPkgPath == "" && newName == "" {
		return fmt.Errorf("nothing to do for %q: give newPkgPath and/or newName", fileName)
	}
	unit, ok := tx.ws.Unit(pkg)
	if !ok {
		return workspace.NoPackageError(pkg)
	}
	for _, owner := range unit.Members() {
		isXTest := owner.ID.Kind() == workspace.KindXTest
		path, err := workspace.NewFilePath(tx.ws.Module(), owner.ID.Base(), fileName)
		if err != nil {
			return err
		}
		if _, ok := owner.File(path); !ok {
			continue
		}
		destOwner := owner
		if newPkgPath != "" {
			destOwner, ok = tx.ws.ProdPackage(newPkgPath)
			if !ok {
				return workspace.NoPackageError(newPkgPath)
			}
		}
		baseName := fileName
		if newName != "" {
			baseName = newName
		}
		newPath, err := workspace.NewFilePath(tx.ws.Module(), destOwner.ID.Base(), baseName)
		if err != nil {
			return err
		}
		if _, _, exists := tx.ws.ResolveFileByPath(newPath); exists {
			return errFileExists(newPath)
		}
		if destOwner == owner {
			tx.ws.MoveFile(pkg, isXTest, path, newPath)
			tx.markChanged(path, newPath)
			return nil
		}
		touched, err := tx.ws.RelocateFile(pkg, path, isXTest, newPkgPath, newPath)
		if err != nil {
			return err
		}
		tx.markChanged(touched...)
		return nil
	}
	return errNoFile(fileName, pkg)
}

// MovePackage moves a package to a new address, rewriting the import
// path in every importer. When the package name equals the old address
// base (the convention), the package clause, every unaliased qualifier,
// and each file's own "Package oldBase" doc-comment opening are renamed
// too; aliased imports keep their alias untouched.
func (tx *Tx) MovePackage(oldPkg, newPkg workspace.PackagePath) error {
	if oldPkg == tx.ws.Module() {
		return fmt.Errorf("no workspace package at %q", oldPkg)
	}
	if newPkg == tx.ws.Module() {
		return workspace.OutsideModuleCreateError(newPkg, tx.ws.Module())
	}
	unit, ok := tx.ws.Unit(oldPkg)
	if !ok {
		return workspace.NoPackageError(oldPkg)
	}
	if _, exists := tx.ws.Unit(newPkg); exists {
		return workspace.PackageExistsError(newPkg)
	}
	oldBase, newBase := oldPkg.Base(), newPkg.Base()
	renameName := unit.Prod() != nil && unit.Prod().Name == oldBase && oldBase != newBase
	if renameName && !token.IsIdentifier(newBase) {
		return workspace.InvalidPackageNameError(newBase)
	}
	touched, err := tx.ws.MovePackage(oldPkg, newPkg, renameName, oldBase, newBase)
	if err != nil {
		return err
	}
	tx.markChanged(touched...)
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
// rename never touches position or order, only relocation can. Relocating
// a member whose meaning depends on its position in the group (iota,
// inherited const values) moves the *whole* group together instead of
// just that member, since extracting it alone would break the positions
// of the rest — Workspace.PositionDependentGroupMembers computes that set
// and DetectMoveConflicts is checked against all of it, not just the
// named key. Cross-package relocation rewrites qualifiers at every
// surviving use site (Workspace.ComputeQualifierFixups): a same-package
// reference gains the destination's qualifier, one already qualified
// toward the destination loses it, and any other qualifier is repointed
// — see relocateSymbols. Moves never cross the test build boundary, and
// the destination file is created when missing.
func (tx *Tx) MoveSymbol(pkg workspace.PackagePath, key string, newPkgPath workspace.PackagePath, newFileName, newSymbolKey string) error {
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
		sym, _, ok := tx.ws.ResolveSymbol(pkg, key)
		if !ok {
			return workspace.NoSymbolError(key, pkg)
		}
		// Captured as a plain value, not read off sym again after the
		// rename: renameSymbol calls applyFileSplices, which can fork the
		// package sym was resolved from, and Recv extracted now stays
		// correct regardless — rename never changes it.
		recv := sym.Recv
		if err := tx.renameSymbol(pkg, key, newName); err != nil {
			return err
		}
		workingKey = workspace.MethodKey(recv, newName)
	}
	if newFileName == "" {
		return nil
	}
	destPkg := pkg
	if newPkgPath != "" {
		destPkg = newPkgPath
	}
	return tx.relocateSymbols(pkg, destPkg, []string{workingKey}, newFileName)
}

// MoveSymbolGroup relocates several symbols from pkg to the same
// destination file in one transaction — the batch counterpart to
// MoveSymbol's single-key path, for moving a type together with its
// methods (or any other explicitly-named set) without a same-package
// consolidation step first. Deliberately narrower than MoveSymbol: no
// combined rename, since renaming applies per-symbol and combining it
// with an N-symbol batch multiplies the interface for a combination
// nobody's asked for — rename first with MoveSymbol, then move. Composes
// on relocateSymbols for the actual mechanics.
func (tx *Tx) MoveSymbolGroup(pkg workspace.PackagePath, keys []string, newPkgPath workspace.PackagePath, newFileName string) error {
	if len(keys) < 2 {
		return fmt.Errorf("MoveSymbolGroup needs at least two keys: moving exactly one symbol already has its own single-key path")
	}
	destPkg := pkg
	if newPkgPath != "" {
		destPkg = newPkgPath
	}
	return tx.relocateSymbols(pkg, destPkg, keys, newFileName)
}

// relocateSymbols is MoveSymbol's and MoveSymbolGroup's shared
// file-relocation half: extract keys' declarations from srcPkg and splice
// them into a file of destPkg (destPkg equals srcPkg for a same-package
// move). destPkg must already exist. Composes on Workspace.RelocateSymbols
// for the actual mechanics — see its own doc comment for the
// conflict/qualifier-fixup guarantees. Private: composed by MoveSymbol
// (single key) and MoveSymbolGroup (several), never called standalone.
func (tx *Tx) relocateSymbols(srcPkg, destPkg workspace.PackagePath, keys []string, fileName string) error {
	destOwner, ok := tx.ws.ProdPackage(destPkg)
	if !ok {
		return workspace.NoPackageError(destPkg)
	}
	destPath, err := workspace.NewFilePath(tx.ws.Module(), destOwner.ID.Base(), fileName)
	if err != nil {
		return err
	}
	touched, err := tx.ws.RelocateSymbols(srcPkg, destPkg, keys, destPath)
	if err != nil {
		return err
	}
	tx.markChanged(touched...)
	return nil
}

// renameSymbol renames key to newName everywhere: the defining identifier,
// its own leading doc-comment mention (when the doc follows Go's
// name-first convention), and every resolved use across the workspace,
// matched by qualified name. Renames exactly this one object — renaming an
// interface method does not chase implementors; broken satisfactions
// arrive in the echo instead. Private: the public verb is MoveSymbol,
// which composes this with relocateSymbols.
func (tx *Tx) renameSymbol(pkg workspace.PackagePath, key, newName string) error {
	if !token.IsIdentifier(newName) {
		return fmt.Errorf("%q is not a valid identifier", newName)
	}
	sym, owner, ok := tx.ws.ResolveSymbol(pkg, key)
	if !ok {
		return workspace.NoSymbolError(key, pkg)
	}
	newKey := workspace.MethodKey(sym.Recv, newName)
	if _, exists := owner.Symbol(newKey); exists {
		return workspace.SymbolExistsError(newKey, pkg)
	}

	var edits workspace.ByteSplices
	def := sym.DefiningIdent()
	if sp, ok := tx.ws.NewSpliceAtPos(owner, sym.File, def.Pos(), def.End(), []byte(newName)); ok {
		edits = append(edits, sp)
	}
	if from, to, ok := workspace.LeadingDocWord(workspace.SymbolDoc(sym), "", sym.Name); ok {
		if sp, ok := tx.ws.NewSpliceAtPos(owner, sym.File, from, to, []byte(newName)); ok {
			edits = append(edits, sp)
		}
	}
	uses, err := tx.ws.ComputeRenameSplices(pkg, key, newName)
	if err != nil {
		return err
	}
	edits = append(edits, uses...)
	touched, err := tx.ws.ApplyFileSplices(edits)
	if err != nil {
		return err
	}
	tx.markChanged(touched...)
	return nil
}

// RepairMissingImports is the bounded self-repair pass behind Store.Edit:
// when a recheck reports "undefined: X" and X names exactly one
// in-memory package, the missing import is spliced in. goimports cannot
// discover packages that exist only in memory (it scans disk), and
// imports are not agent-addressable, so the server must cover its own
// blind spot. Best-effort by design: ambiguous names and failed splices
// leave the diagnostic standing, and a wrong repair (an ident that merely
// collides with a package name) surfaces as an ordinary diagnostic on the
// next echo while goimports drops the then-unused import on the file's
// next reload.
func (tx *Tx) RepairMissingImports() bool {
	// Unique importable package names known to the workspace.
	candidates := make(map[string]workspace.PackagePath) // package name -> import path
	ambiguous := make(map[string]bool)
	for _, addr := range tx.ws.UnitKeys() {
		unit, _ := tx.ws.Unit(addr)
		pkg := unit.Prod()
		if pkg == nil || pkg.ID.Base() == "" || pkg.Name == "main" {
			continue
		}
		if _, dup := candidates[pkg.Name]; dup {
			ambiguous[pkg.Name] = true
			delete(candidates, pkg.Name)
			continue
		}
		candidates[pkg.Name] = pkg.ID.Base()
	}

	needed := make(map[workspace.FilePath]map[string]bool) // file -> import paths
	for _, diag := range tx.AllDiagnostics() {
		if diag.Kind != workspace.DiagType || diag.File == "" {
			continue
		}
		name, found := strings.CutPrefix(diag.Msg, "undefined: ")
		if !found || !token.IsIdentifier(name) || ambiguous[name] {
			continue
		}
		path, ok := candidates[name]
		if !ok {
			continue
		}
		file, owner, ok := tx.ws.ResolveFileByPath(diag.File)
		if !ok || owner.ID.Base() == path || workspace.ImportsPath(file.Ast(), string(path)) {
			continue
		}
		if needed[diag.File] == nil {
			needed[diag.File] = make(map[string]bool)
		}
		needed[diag.File][string(path)] = true
	}

	repaired := false
	for _, filePath := range sortedKeys(needed) {
		file, owner, ok := tx.ws.ResolveFileByPath(filePath)
		if !ok {
			continue
		}
		var repl strings.Builder
		for _, path := range sortedKeys(needed[filePath]) {
			fmt.Fprintf(&repl, "\n\nimport %q", path)
		}
		insertAt := file.Ast().Name.End()
		sp, ok := tx.ws.NewSpliceAtPos(owner, filePath, insertAt, insertAt, []byte(repl.String()))
		if !ok {
			continue
		}
		candidate := workspace.ByteSplices{sp}.Apply(file.Src())
		addr := filePath.PackagePath()
		if err := tx.ws.SwapFile(addr, owner.ID.Kind() == workspace.KindXTest, filePath, candidate); err != nil {
			continue // repair is best-effort; the diagnostic stays visible
		}
		tx.markChanged(filePath)
		repaired = true
	}
	return repaired
}

// markChanged records paths as changed by this transaction; every verb reports
// its footprint here regardless of prior dirtiness.
func (tx *Tx) markChanged(paths ...workspace.FilePath) {
	for _, path := range paths {
		tx.changed[path] = true
	}
}
