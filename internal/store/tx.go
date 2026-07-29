package store

import (
	"fmt"
	"go/token"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// CreateFile adds an empty file to an existing package, optionally seeded
// with a package doc comment. pkg may name a unit's XTest half via its
// own "_test"-suffixed address (workspace.Workspace.EnsurePackage),
// installing that half the first time something targets it.
func (tx *Tx) CreateFile(pkg address.PkgPath, name, doc string) error {
	canon, p, isXTest, err := tx.ws.EnsurePackage(pkg)
	if err != nil {
		return err
	}
	path, err := address.NewFilePath(tx.ws.Module(), canon, name)
	if err != nil {
		return err
	}
	if _, _, exists := tx.ws.ResolveFileByPath(path); exists {
		return fmt.Errorf("file %q already exists", path)
	}
	content := string(renderDocComment(doc)) + "package " + p.Name + "\n"
	return tx.installFile(canon, isXTest, path, []byte(content))
}

// CreatePackage creates a new package at a module-prefixed address with one
// file named after the package. name defaults to the address base. Fails if
// the address already holds a package; the directory is created at Flush.
func (tx *Tx) CreatePackage(pkg address.PkgPath, name string) error {
	if pkg == tx.ws.Module() {
		return fmt.Errorf("cannot create a package at %q: workspace packages live under module %q", pkg, tx.ws.Module())
	}
	if _, exists := tx.ws.Unit(pkg); exists {
		return fmt.Errorf("a package already exists at %q", pkg)
	}
	if name == "" {
		name = pkg.Base()
	}
	if !token.IsIdentifier(name) {
		return fmt.Errorf("%q is not a valid package name", name)
	}
	tx.ws.InstallUnit(pkg, workspace.NewUnit(workspace.NewPackage(name, pkg, nil, nil, false, false), nil))
	return tx.installFile(pkg, false, pkg.File(name+".go"), []byte("package "+name+"\n"))
}

// CreateSymbol adds one new top-level declaration to a file of an existing
// package, at its canonical position. pkg may name a unit's XTest half
// via its own "_test"-suffixed address (workspace.Workspace.EnsurePackage),
// installing that half the first time something targets it. The file is
// required, never inferred — but a missing file inside the package is
// created implicitly, since creation cannot destroy. A new plain
// (non-position-dependent) const or var merges into the file's existing
// grouped block of the same kind, if one already exists — keeping
// placement decisions meaningful instead of proliferating interchangeable,
// unaddressable group shells; a new group is only created when none
// exists yet, and a standalone declaration is never retroactively
// converted into one. A new iota (position-dependent) group never merges
// — it always starts its own — and is placed next to its shared type's
// own declaration when typed and that type is in this file, the same
// clustering declPrecedes already gives methods with their receiver;
// otherwise it falls to the standard const/var region, same as an
// untyped iota group always does.
func (tx *Tx) CreateSymbol(pkg address.PkgPath, fileName, src string) error {
	canon, p, isXTest, err := tx.ws.EnsurePackage(pkg)
	if err != nil {
		return err
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
	path, err := address.NewFilePath(tx.ws.Module(), canon, fileName)
	if err != nil {
		return err
	}
	file, ok := p.File(path)
	if !ok {
		candidate := []byte("package " + p.Name + "\n\n" + src + "\n")
		return tx.installFile(canon, isXTest, path, candidate)
	}

	if (frag.kind == dto.KindConst || frag.kind == dto.KindVar) && !frag.usesIota {
		tok := token.CONST
		if frag.kind == dto.KindVar {
			tok = token.VAR
		}
		if at, ok := tx.ws.MergeableGroupInsertOffset(canon, path, tok); ok {
			specs, _, err := constVarEntries(src)
			if err != nil {
				return err
			}
			sp, ok := tx.ws.NewSpliceAtOffset(p, path, at, at, []byte("\n"+specs+"\n"))
			if !ok {
				return fmt.Errorf("cannot locate insertion point in %q", path)
			}
			return tx.installFile(canon, isXTest, path, workspace.ApplySplices(file.Src(), []workspace.Splice{sp}))
		}
	}

	at, ok := tx.ws.InsertOffset(canon, path, workspace.SymbolKind(frag.kind), frag.recv)
	if !ok {
		return fmt.Errorf("cannot locate insertion point in %q", path)
	}
	if frag.kind == dto.KindConst && frag.usesIota {
		if _, typeName, terr := constVarEntries(src); terr == nil && typeName != "" {
			if anchor, ok := tx.ws.TypeDeclOffset(canon, path, typeName); ok {
				at = anchor
			}
		}
	}
	sp, ok := tx.ws.NewSpliceAtOffset(p, path, at, at, []byte("\n\n"+src+"\n"))
	if !ok {
		return fmt.Errorf("cannot locate insertion point in %q", path)
	}
	return tx.installFile(canon, isXTest, path, workspace.ApplySplices(file.Src(), []workspace.Splice{sp}))
}

// DeleteFile removes one file and every declaration in it, tombstoning the
// path for Flush. Deletion is idempotent: a missing package or file is a
// noop, not a failure — the file being gone is the success condition,
// whoever caused it.
func (tx *Tx) DeleteFile(pkg address.PkgPath, name string) error {
	unit, ok := tx.ws.Unit(pkg)
	if !ok {
		return nil
	}
	for _, owner := range unit.Members() {
		path, err := address.NewFilePath(tx.ws.Module(), owner.PkgPath, name)
		if err != nil {
			return err
		}
		if _, ok := owner.File(path); !ok {
			continue
		}
		tx.ws.DropFile(pkg, owner.IsXTest, path)
		tx.markChanged(path)
		return nil
	}
	return nil
}

// DeletePackage removes a whole package address, tombstoning every file.
// Deletion is idempotent: a missing package is a noop, not a failure.
func (tx *Tx) DeletePackage(pkg address.PkgPath) error {
	unit, ok := tx.ws.Unit(pkg)
	if !ok {
		return nil
	}
	for _, p := range unit.Members() {
		for _, file := range p.Files() {
			tx.ws.Tombstone(pkg, file.Path, p.Name)
			tx.markChanged(file.Path)
		}
	}
	tx.ws.RemoveUnit(pkg)
	return nil
}

// DeleteSymbol removes key's declaration — its spec alone when it lives in
// a grouped declaration with siblings, unless its value is derived from
// its position (iota, or inheriting the previous spec's expression), in
// which case the whole group is removed together. Deleting one member of
// a position-dependent group and leaving the rest as-is has no single
// correct resolution (keep everyone else's original values? renumber
// them?) — that's edit_symbol's job, via a whole-group replacement that
// states explicitly what the agent wants, not a guess this verb would
// have to make.
//
// A name sharing a *ast.ValueSpec with others (`var a, b int`, `var a, b
// = f()`) is trimmed from the spec instead of taking the others down with
// it — see Workspace.DeletionSplices. Once no real name remains, the spec
// collapses to a full removal, same as deleting a solo name.
//
// Deletion is idempotent: a missing symbol is a noop, not a failure.
func (tx *Tx) DeleteSymbol(pkg address.PkgPath, key string) error {
	splices, found, err := tx.ws.ComputeDeletionSplices(pkg, key)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	path := splices[0].Path
	file, owner, ok := tx.ws.ResolveFileByPath(path)
	if !ok {
		return fmt.Errorf("internal error: %q vanished while deleting %q", path, key)
	}
	return tx.installFile(pkg, owner.IsXTest, path, workspace.ApplySplices(file.Src(), splices))
}

// EditFile replaces or clears a file's package doc comment — the comment
// block directly above the package clause — leaving the rest of the file
// untouched. The one sanctioned door into floating-comment space: every
// other comment stays unaddressable by design.
func (tx *Tx) EditFile(pkg address.PkgPath, name, doc string) error {
	p, ok := tx.ws.ProdPackage(pkg)
	if !ok {
		return fmt.Errorf("no package at %q", pkg)
	}
	path, err := address.NewFilePath(tx.ws.Module(), p.PkgPath, name)
	if err != nil {
		return err
	}
	file, _, ok := tx.ws.ResolveFileByPath(path)
	if !ok {
		return fmt.Errorf("no file %q in %q", name, pkg)
	}
	astFile := file.Ast()
	docPos, docEnd := astFile.Package, astFile.Package
	if astFile.Doc != nil {
		docPos = astFile.Doc.Pos()
	}
	sp, ok := tx.ws.NewSpliceAtPos(p, path, docPos, docEnd, renderDocComment(doc))
	if !ok {
		return fmt.Errorf("cannot locate doc comment span in %q", path)
	}
	candidate := workspace.ApplySplices(file.Src(), []workspace.Splice{sp})
	return tx.installFile(pkg, false, path, candidate)
}

// EditSymbol replaces key's whole declaration with src — for members of
// grouped declarations, src is the member's spec as written inside the
// group. A replacement may rename; the new key must not collide.
// For a member of a position-dependent const group (iota, or inheriting
// the previous spec's expression), src must be the group's whole
// intended state — every member's spec, not just key's own, still bare
// (no group keyword/parens — those are reconstructed) — since a partial
// replacement would silently drop whatever isn't mentioned; the
// targeted key itself must still be present, or the edit is refused (use
// MoveSymbol to rename a group member instead, which propagates
// references correctly and is the only tool that can). Editing a
// non-position-dependent group member to introduce iota is refused —
// that converts the group's structure, not just one value, and isn't
// supported through a single member's replacement.
func (tx *Tx) EditSymbol(pkg address.PkgPath, key, src string) error {
	wasPositionDependent, groupTok, target, err := tx.ws.ComputeEditPlan(pkg, key)
	if err != nil {
		return err
	}
	var frag fragment
	replacement := src
	switch {
	case wasPositionDependent:
		frag, err = parseSpecFragment(groupTok, src)
		replacement = groupTok.String() + " (\n" + src + "\n)"
	case groupTok != token.ILLEGAL:
		frag, err = parseSpecFragment(groupTok, src)
		if err == nil && frag.usesIota {
			err = fmt.Errorf("%q would introduce iota into a group that doesn't use it: converting a plain group into a position-dependent one isn't supported through edit_symbol", key)
		}
	default:
		frag, err = parseDeclFragment(src)
	}
	if err != nil {
		return err
	}
	if wasPositionDependent && !slices.Contains(frag.keys, key) {
		return fmt.Errorf("%q is missing from the replacement: a position-dependent group member can't be renamed through edit_symbol, use refactor_move_symbol instead", key)
	}
	if collisions := tx.ws.DetectEditCollisions(pkg, key, frag.keys); len(collisions) > 0 {
		return fmt.Errorf("replacement declares %q, which already exists in %q", collisions[0], pkg)
	}
	file, owner, ok := tx.ws.ResolveFileByPath(target.Path)
	if !ok {
		return fmt.Errorf("internal error: %q vanished while editing %q", target.Path, key)
	}
	target.Repl = []byte(replacement)
	return tx.installFile(pkg, owner.IsXTest, target.Path, workspace.ApplySplices(file.Src(), []workspace.Splice{target}))
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
func (tx *Tx) MoveFile(pkg address.PkgPath, fileName string, newPkgPath address.PkgPath, newName string) error {
	if newPkgPath == "" && newName == "" {
		return fmt.Errorf("nothing to do for %q: give newPkgPath and/or newName", fileName)
	}
	unit, ok := tx.ws.Unit(pkg)
	if !ok {
		return fmt.Errorf("no package at %q", pkg)
	}
	for _, owner := range unit.Members() {
		isXTest := owner.IsXTest
		path, err := address.NewFilePath(tx.ws.Module(), owner.PkgPath, fileName)
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
				return fmt.Errorf("no package at %q: create_package first", newPkgPath)
			}
		}
		baseName := fileName
		if newName != "" {
			baseName = newName
		}
		newPath, err := address.NewFilePath(tx.ws.Module(), destOwner.PkgPath, baseName)
		if err != nil {
			return err
		}
		if _, _, exists := tx.ws.ResolveFileByPath(newPath); exists {
			return fmt.Errorf("file %q already exists", newPath)
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
	return fmt.Errorf("no file %q in %q", fileName, pkg)
}

// MovePackage moves a package to a new address, rewriting the import
// path in every importer. When the package name equals the old address
// base (the convention), the package clause, every unaliased qualifier,
// and each file's own "Package oldBase" doc-comment opening are renamed
// too; aliased imports keep their alias untouched.
func (tx *Tx) MovePackage(oldPkg, newPkg address.PkgPath) error {
	if oldPkg == tx.ws.Module() {
		return fmt.Errorf("no workspace package at %q", oldPkg)
	}
	if newPkg == tx.ws.Module() {
		return fmt.Errorf("cannot move %q to %q: workspace packages live under module %q", oldPkg, newPkg, tx.ws.Module())
	}
	unit, ok := tx.ws.Unit(oldPkg)
	if !ok {
		return fmt.Errorf("no package at %q", oldPkg)
	}
	if _, exists := tx.ws.Unit(newPkg); exists {
		return fmt.Errorf("a package already exists at %q", newPkg)
	}
	oldBase, newBase := oldPkg.Base(), newPkg.Base()
	renameName := unit.Prod() != nil && unit.Prod().Name == oldBase && oldBase != newBase
	if renameName && !token.IsIdentifier(newBase) {
		return fmt.Errorf("%q is not a valid package name", newBase)
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
// — see relocateSymbol. Moves never cross the test build boundary, and
// the destination file is created when missing.
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
		sym, _, ok := tx.ws.ResolveSymbol(pkg, key)
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

// MoveSymbolGroup relocates several symbols from pkg to the same
// destination file in one transaction — the batch counterpart to
// MoveSymbol's single-key path, for moving a type together with its
// methods (or any other explicitly-named set) without a same-package
// consolidation step first. Deliberately narrower than MoveSymbol: no
// combined rename, since renaming applies per-symbol and combining it
// with an N-symbol batch multiplies the interface for a combination
// nobody's asked for — rename first with MoveSymbol, then move. Composes
// on Workspace.RelocateSymbols for the actual mechanics.
func (tx *Tx) MoveSymbolGroup(pkg address.PkgPath, keys []string, newPkgPath address.PkgPath, newFileName string) error {
	if len(keys) < 2 {
		return fmt.Errorf("MoveSymbolGroup needs at least two symbol_keys; refactor_move_symbol's single-key path already covers one")
	}
	destPkg := pkg
	if newPkgPath != "" {
		destPkg = newPkgPath
	}
	destOwner, ok := tx.ws.ProdPackage(destPkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", destPkg)
	}
	destPath, err := address.NewFilePath(tx.ws.Module(), destOwner.PkgPath, newFileName)
	if err != nil {
		return err
	}
	touched, err := tx.ws.RelocateSymbols(pkg, destPkg, keys, destPath)
	if err != nil {
		return err
	}
	tx.markChanged(touched...)
	return nil
}

// relocateSymbol is MoveSymbol's file-relocation half: extract key's
// declaration from srcPkg and splice it into a file of destPkg (destPkg
// equals srcPkg for a same-package move). destPkg must already exist.
// Composes on Workspace.RelocateSymbols for the actual mechanics — see
// its own doc comment for the conflict/qualifier-fixup guarantees.
// Private: composed by MoveSymbol, never called standalone.
func (tx *Tx) relocateSymbol(srcPkg, destPkg address.PkgPath, key, fileName string) error {
	destOwner, ok := tx.ws.ProdPackage(destPkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", destPkg)
	}
	destPath, err := address.NewFilePath(tx.ws.Module(), destOwner.PkgPath, fileName)
	if err != nil {
		return err
	}
	touched, err := tx.ws.RelocateSymbols(srcPkg, destPkg, []string{key}, destPath)
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
// which composes this with relocateSymbol.
func (tx *Tx) renameSymbol(pkg address.PkgPath, key, newName string) error {
	if !token.IsIdentifier(newName) {
		return fmt.Errorf("%q is not a valid identifier", newName)
	}
	sym, owner, ok := tx.ws.ResolveSymbol(pkg, key)
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

	var edits []workspace.Splice
	def := sym.DefiningIdent()
	if sp, ok := tx.ws.NewSpliceAtPos(owner, sym.File, def.Pos(), def.End(), []byte(newName)); ok {
		edits = append(edits, sp)
	}
	if from, to, ok := workspace.LeadingDocWord(symbolDoc(sym), "", sym.Name); ok {
		if sp, ok := tx.ws.NewSpliceAtPos(owner, sym.File, from, to, []byte(newName)); ok {
			edits = append(edits, sp)
		}
	}
	uses, err := tx.ws.ComputeRenameSplices(pkg, key, newName)
	if err != nil {
		return err
	}
	edits = append(edits, uses...)
	return tx.applyFileSplices(edits)
}
