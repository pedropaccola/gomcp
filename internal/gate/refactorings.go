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
	for i, owner := range []*workspace.Package{unit.Prod(), unit.XTest()} {
		if owner == nil {
			continue
		}
		isXTest := i == 1
		path, err := address.NewFilePath(tx.ws.Module(), owner.PkgPath, fileName)
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
		newPath, err := address.NewFilePath(tx.ws.Module(), destOwner.PkgPath, baseName)
		if err != nil {
			return err
		}
		if _, _, exists := tx.resolveFileByPath(newPath); exists {
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
		if conflicts := tx.ws.DetectMoveConflicts(pkg, newPkgPath, movingKeys); len(conflicts) > 0 {
			return fmt.Errorf("moving %q to %q would break the workspace: %s", fileName, newPkgPath, strings.Join(conflicts, "; "))
		}
		fixups, ferr := tx.ws.ComputeQualifierFixups(pkg, newPkgPath, movingKeys)
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
		return tx.installFile(newPkgPath, false, newPath, candidate)
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
	if !ok || newDir == "." || address.IsOutsideRoot(newDir) {
		return fmt.Errorf("cannot move %q to %q: workspace packages live under module %q", oldPkg, newPkg, tx.ws.Module())
	}
	unit, ok := tx.ws.Unit(oldPkg)
	if !ok {
		return fmt.Errorf("no package at %q", oldPkg)
	}
	if _, exists := tx.ws.Unit(newPkg); exists {
		return fmt.Errorf("a package already exists at %q", newPkg)
	}
	oldBase, newBase := filepath.Base(dir), filepath.Base(newDir)
	renameName := unit.Prod() != nil && unit.Prod().Name == oldBase && oldBase != newBase
	if renameName && !token.IsIdentifier(newBase) {
		return fmt.Errorf("%q is not a valid package name", newBase)
	}

	edits := tx.ws.ComputePackageMoveSplices(oldPkg, newPkg, renameName, oldBase, newBase)
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
	tx.ws.InstallUnit(newPkg, workspace.NewUnit(halves[0].moved, halves[1].moved))
	for i, h := range halves {
		if h.orig == nil {
			continue
		}
		isXTest := i == 1
		for _, file := range h.orig.Files() {
			newPath := newPkg.File(file.Path.Name())
			tx.ws.Tombstone(oldPkg, file.Path, h.orig.Name)
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
			if err := tx.installFile(newPkg, isXTest, newPath, candidate); err != nil {
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
// Cross-package relocation is refused when Workspace.DetectMoveConflicts
// can prove in advance it would break the workspace — checked against
// every key Workspace.PositionDependentGroupMembers says will actually
// move, not just the one named, since a position-dependent const group
// moves as a whole and the safety check must see exactly what
// relocateDeclaration is about to act on. Otherwise every surviving
// reference across the move boundary has its qualifier fixed up first
// (Workspace.ComputeQualifierFixups), so both the declaration's callers
// and the declaration's own outbound references keep resolving from
// their new vantage point — see applyQualifierFixups and
// relocateDeclaration for the shared mechanics MoveSymbolGroup also
// composes on. Private: composed by MoveSymbol, never called standalone.
func (tx *Tx) relocateSymbol(srcPkg, destPkg address.PkgPath, key, fileName string) error {
	destOwner, ok := tx.resolvePackage(destPkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", destPkg)
	}
	destPath, err := address.NewFilePath(tx.ws.Module(), destOwner.PkgPath, fileName)
	if err != nil {
		return err
	}
	movingKeys, err := tx.ws.PositionDependentGroupMembers(srcPkg, key)
	if err != nil {
		return err
	}
	if conflicts := tx.ws.DetectMoveConflicts(srcPkg, destPkg, movingKeys); len(conflicts) > 0 {
		return fmt.Errorf("moving %q to %q would break the workspace: %s", key, destPkg, strings.Join(conflicts, "; "))
	}
	if err := tx.applyQualifierFixups(srcPkg, destPkg, movingKeys); err != nil {
		return err
	}
	return tx.relocateDeclaration(srcPkg, destPkg, key, destPath)
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

	edits := make(map[address.FilePath][]splice)
	def := sym.DefiningIdent()
	if sp, ok := tx.offsetSpan(sym.File, def.Pos(), def.End()); ok {
		edits[sym.File] = append(edits[sym.File], splice{span: sp, repl: []byte(newName)})
	}
	if sp, ok := tx.leadingDocWord(sym.File, symbolDoc(sym), "", sym.Name); ok {
		edits[sym.File] = append(edits[sym.File], splice{span: sp, repl: []byte(newName)})
	}
	uses, err := tx.ws.ComputeRenameSplices(pkg, key, newName)
	if err != nil {
		return err
	}
	for path, splices := range toSplices(uses) {
		edits[path] = append(edits[path], splices...)
	}
	return tx.applyFileSplices(edits)
}

// applyQualifierFixups computes and applies the splices Workspace.
// QualifierFixups says are needed for movingKeys leaving srcPkg for
// destPkg — a no-op for a same-package move. Shared by relocateSymbol
// and MoveSymbolGroup so a batch of several keys gets exactly one
// fixup pass over the whole set, not one per key.
func (tx *Tx) applyQualifierFixups(srcPkg, destPkg address.PkgPath, movingKeys []string) error {
	if srcPkg == destPkg {
		return nil
	}
	fixups, err := tx.ws.ComputeQualifierFixups(srcPkg, destPkg, movingKeys)
	if err != nil {
		return err
	}
	if len(fixups) == 0 {
		return nil
	}
	return tx.applyFileSplices(toSplices(fixups))
}

// relocateDeclaration extracts key's own declaration from srcPkg and
// splices it into destPath (already resolved, already confirmed to
// belong to destPkg) — the mechanical half of a relocation, with no
// safety checks of its own beyond the two structural guards every
// relocation needs regardless of scope (already-there, test-boundary
// crossing): callers (relocateSymbol, MoveSymbolGroup) are responsible
// for DetectMoveConflicts and applyQualifierFixups first, and only once,
// up front — not here, since a batch relocates several keys one at a
// time and an already-relocated key no longer resolves from srcPkg, so a
// conflict check repeated mid-batch would incorrectly see it as left
// behind. Always resolves srcPkg/key and destPkg fresh from tx.ws — never
// a pointer a caller might be holding from before an earlier key's own
// installFile in the same batch forked the package out from under it.
func (tx *Tx) relocateDeclaration(srcPkg, destPkg address.PkgPath, key string, destPath address.FilePath) error {
	sym, owner, ok := tx.resolveSymbol(srcPkg, key)
	if !ok {
		return fmt.Errorf("no symbol %q in %q", key, srcPkg)
	}
	destOwner, ok := tx.resolvePackage(destPkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", destPkg)
	}
	if destOwner == owner && destPath == sym.File {
		return fmt.Errorf("%q already lives in %q", key, destPath)
	}
	if strings.HasSuffix(destPath.String(), "_test.go") != strings.HasSuffix(sym.File.String(), "_test.go") {
		return fmt.Errorf("moving %q from %q to %q would cross the test build boundary", key, sym.File, destPath)
	}
	srcIsXTest := tx.isXTestOwner(srcPkg, owner)
	src, extractSplice, err := tx.ws.ExtractDeclaration(srcPkg, key)
	if err != nil {
		return err
	}
	frag, err := parseDeclFragment(src)
	if err != nil {
		return err
	}
	dest, inDest := destOwner.File(destPath)
	if _, _, exists := tx.resolveFileByPath(destPath); exists && !inDest {
		return fmt.Errorf("file %q belongs to another package", destPath)
	}
	file, _ := owner.File(sym.File)
	if err := tx.installFile(srcPkg, srcIsXTest, sym.File, applySplices(file.Src(), []splice{{span: span{start: extractSplice.Start, end: extractSplice.End}}})); err != nil {
		return err
	}
	if !inDest {
		return tx.installFile(destPkg, false, destPath, []byte("package "+destOwner.Name+"\n\n"+src+"\n"))
	}
	dest, _, ok = tx.resolveFileByPath(destPath)
	if !ok {
		return fmt.Errorf("internal error: %q vanished after relocation", destPath)
	}
	at, ok := tx.ws.InsertOffset(destPkg, destPath, workspace.SymbolKind(frag.kind), frag.recv)
	if !ok {
		return fmt.Errorf("cannot locate insertion point in %q", destPath)
	}
	return tx.installFile(destPkg, false, destPath, applySplices(dest.Src(), []splice{{span: span{start: at, end: at}, repl: []byte("\n\n" + src + "\n")}}))
}

// MoveSymbolGroup relocates several symbols from pkg to the same
// destination file in one transaction — the batch counterpart to
// MoveSymbol's single-key path, for moving a type together with its
// methods (or any other explicitly-named set) without a same-package
// consolidation step first. Deliberately narrower than MoveSymbol: no
// combined rename, since renaming applies per-symbol and combining it
// with an N-symbol batch multiplies the interface for a combination
// nobody's asked for — rename first with MoveSymbol, then move. Every
// key's own position-dependent group (Workspace.
// PositionDependentGroupMembers) is unioned into the moving set before
// DetectMoveConflicts sees any of it, so a batch that happens to include
// an iota member is exactly as safe as the single-key path. Extraction
// then collapses to one representative key per position-dependent group
// — ExtractDeclaration already pulls a whole such group's text from any
// one member, so calling relocateDeclaration for every member of the
// same group would try to re-resolve siblings the first call already
// spliced away. Types are placed before their own methods regardless of
// input order, so InsertOffset's "attach to your receiver" placement
// resolves correctly for a method landing right after the type it just
// moved in with.
func (tx *Tx) MoveSymbolGroup(pkg address.PkgPath, keys []string, newPkgPath address.PkgPath, newFileName string) error {
	if len(keys) < 2 {
		return fmt.Errorf("MoveSymbolGroup needs at least two symbol_keys; refactor_move_symbol's single-key path already covers one")
	}
	destPkg := pkg
	if newPkgPath != "" {
		destPkg = newPkgPath
	}
	destOwner, ok := tx.resolvePackage(destPkg)
	if !ok {
		return fmt.Errorf("no package at %q: create_package first", destPkg)
	}
	destPath, err := address.NewFilePath(tx.ws.Module(), destOwner.PkgPath, newFileName)
	if err != nil {
		return err
	}

	seen := make(map[string]bool, len(keys))
	var movingKeys []string
	for _, key := range keys {
		members, err := tx.ws.PositionDependentGroupMembers(pkg, key)
		if err != nil {
			return err
		}
		for _, m := range members {
			if !seen[m] {
				seen[m] = true
				movingKeys = append(movingKeys, m)
			}
		}
	}
	if conflicts := tx.ws.DetectMoveConflicts(pkg, destPkg, movingKeys); len(conflicts) > 0 {
		return fmt.Errorf("moving %v to %q would break the workspace: %s", keys, destPkg, strings.Join(conflicts, "; "))
	}
	if err := tx.applyQualifierFixups(pkg, destPkg, movingKeys); err != nil {
		return err
	}

	claimed := make(map[string]bool, len(movingKeys))
	var representatives []string
	for _, key := range movingKeys {
		if claimed[key] {
			continue
		}
		group, err := tx.ws.PositionDependentGroupMembers(pkg, key)
		if err != nil {
			return err
		}
		for _, m := range group {
			claimed[m] = true
		}
		representatives = append(representatives, key)
	}

	ordered := make([]string, 0, len(representatives))
	for _, key := range representatives {
		if sym, _, ok := tx.resolveSymbol(pkg, key); ok && sym.Kind == workspace.KindType {
			ordered = append(ordered, key)
		}
	}
	for _, key := range representatives {
		if sym, _, ok := tx.resolveSymbol(pkg, key); ok && sym.Kind != workspace.KindType {
			ordered = append(ordered, key)
		}
	}
	for _, key := range ordered {
		if err := tx.relocateDeclaration(pkg, destPkg, key, destPath); err != nil {
			return err
		}
	}
	return nil
}
