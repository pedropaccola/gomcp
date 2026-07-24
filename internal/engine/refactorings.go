package engine

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
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
// given. Moving into a different package is refused when it would break
// something moveConflicts can prove in advance — a method left without
// its receiver type, a name collision at the destination, the moved code
// depending on an unexported sibling staying behind, or code staying
// behind depending on an unexported declaration that's leaving. Otherwise
// every surviving reference across the move boundary has its qualifier
// fixed up first (qualifierFixups) — external callers of the file's
// exported declarations, and the file's own outbound references to
// exported siblings staying behind, alike.
func (tx *Tx) MoveFile(pkg address.PkgPath, fileName string, newPkgPath address.PkgPath, newName string) error {
	if newPkgPath == "" && newName == "" {
		return fmt.Errorf("nothing to do for %q: give newPkgPath and/or newName", fileName)
	}
	unit, ok := tx.ws.Unit(pkg)
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
			tx.ws.MoveFile(owner, path, newPath)
			tx.touch(path, newPath)
			return nil
		}
		var moving []*workspace.Symbol
		for _, sym := range owner.Symbols() {
			if sym.File == path {
				moving = append(moving, sym)
			}
		}
		if conflicts := tx.moveConflicts(pkg, newPkgPath, moving); len(conflicts) > 0 {
			return fmt.Errorf("moving %q to %q would break the workspace: %s", fileName, newPkgPath, strings.Join(conflicts, "; "))
		}
		fixups, ferr := tx.qualifierFixups(moving, pkg, newPkgPath)
		if ferr != nil {
			return ferr
		}
		if len(fixups) > 0 {
			if err := tx.applyFileSplices(fixups); err != nil {
				return err
			}
		}
		file, _ := owner.File(path)
		candidate := file.Src()
		if sp, ok := tx.offsetSpan(path, file.Ast().Name.Pos(), file.Ast().Name.End()); ok {
			candidate = applySplices(candidate, []splice{{span: sp, repl: []byte(destOwner.Name)}})
		}
		tx.ws.DropFile(pkg, owner, path)
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
	dir, ok := tx.dirOf(oldPkg)
	if !ok || dir == "." {
		return fmt.Errorf("no workspace package at %q", oldPkg)
	}
	newDir, ok := tx.dirOf(newPkg)
	if !ok || newDir == "." || newDir.EscapesRoot() {
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
				relFile, err := tx.eng.relativePath(tx.ws.FileSet().Position(ident.Pos()).Filename)
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
			tx.ws.Tombstone(file.Path, pkg.Name)
			tx.ws.ClearTombstone(newPath)
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
	tx.ws.RemoveUnit(oldPkg)
	tx.ws.InstallUnit(newPkg, newUnit)
	return nil
}

// relocateSymbol is MoveSymbol's file-relocation half: extract key's
// declaration from srcPkg and splice it into a file of destPkg (destPkg
// equals srcPkg for a same-package move). destPkg must already exist.
// Cross-package relocation is refused when moveConflicts can prove in
// advance it would break the workspace; otherwise every surviving
// reference across the move boundary has its qualifier fixed up first
// (qualifierFixups), so both the declaration's callers and the
// declaration's own outbound references keep resolving from their new
// vantage point. Private: composed by MoveSymbol, never called standalone.
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
	if conflicts := tx.moveConflicts(srcPkg, destPkg, []*workspace.Symbol{sym}); len(conflicts) > 0 {
		return fmt.Errorf("moving %q to %q would break the workspace: %s", key, destPkg, strings.Join(conflicts, "; "))
	}
	if srcPkg != destPkg {
		fixups, ferr := tx.qualifierFixups([]*workspace.Symbol{sym}, srcPkg, destPkg)
		if ferr != nil {
			return ferr
		}
		if len(fixups) > 0 {
			if err := tx.applyFileSplices(fixups); err != nil {
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
		}
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

// moveConflicts reports every reason relocating moving (all currently
// declared in srcPkg) to destPkg would break something — nil means the
// move is safe. Same-package moves are always safe: nothing about
// visibility or receiver locality changes when the package doesn't.
//
// Checked in order, cheapest and most unconditional first:
//  1. Method receiver locality, both directions: a method's receiver type
//     must be declared in the same package as the method — not a
//     visibility question, just illegal Go otherwise. A moving method
//     needs its receiver type moving too; symmetrically, a method staying
//     behind needs its receiver type staying too.
//  2. Collision: does destPkg already declare something by this name?
//  3. Dependency: does a moving declaration reference an unexported
//     package-level sibling staying behind in srcPkg? (Local variables
//     and parameters are unexported too by convention, but aren't
//     workspace symbols and travel with the declaration regardless —
//     excluded by requiring the object's parent scope to be the package
//     scope itself, not some inner function/block scope.)
//  4. Blocking referrer: does code staying behind in srcPkg reference an
//     unexported symbol that's leaving? (An exported one leaving is a
//     fixup, not a conflict — see qualifierFixups.)
func (tx *Tx) moveConflicts(srcPkg, destPkg address.PkgPath, moving []*workspace.Symbol) []string {
	if srcPkg == destPkg {
		return nil
	}
	movingKeys := make(map[string]bool, len(moving))
	movingNames := make(map[string]bool, len(moving))
	for _, sym := range moving {
		movingKeys[objKey(tx.objectOf(sym))] = true
		movingNames[sym.Name] = true
	}

	var conflicts []string
	destOwner, destExists := tx.resolvePackage(destPkg)
	for _, sym := range moving {
		if sym.Kind == workspace.KindMethod && !movingNames[sym.Recv] {
			conflicts = append(conflicts, fmt.Sprintf(
				"%q is a method: its receiver type %q must move with it, but %q isn't part of this move",
				sym.Key(), sym.Recv, sym.Recv))
			continue
		}
		if destExists {
			if _, exists := destOwner.Symbol(sym.Key()); exists {
				conflicts = append(conflicts, fmt.Sprintf("%q already exists in %q", sym.Key(), destPkg))
			}
		}
	}

	srcOwner, ok := tx.resolvePackage(srcPkg)
	if ok {
		for _, s := range srcOwner.Symbols() {
			if s.Kind != workspace.KindMethod || movingKeys[objKey(tx.objectOf(s))] {
				continue // not a method, or already moving with its receiver
			}
			if movingNames[s.Recv] {
				conflicts = append(conflicts, fmt.Sprintf(
					"%q would be left behind without its receiver type %q, which is moving to %q",
					s.Key(), s.Recv, destPkg))
			}
		}
	}

	if ok && srcOwner.TypesInfo() != nil {
		pkgScope := srcOwner.Types().Scope()
		for _, sym := range moving {
			ast.Inspect(sym.Decl(), func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				obj := srcOwner.TypesInfo().Uses[ident]
				if obj == nil || obj.Pkg() == nil || obj.Parent() != pkgScope {
					return true
				}
				if obj.Pkg().Path() == string(srcPkg) && !obj.Exported() && !movingKeys[objKey(obj)] {
					conflicts = append(conflicts, fmt.Sprintf(
						"%q depends on unexported %q, which stays in %q",
						sym.Key(), obj.Name(), srcPkg))
				}
				return true
			})
		}
	}

	for _, sym := range moving {
		obj := tx.objectOf(sym)
		if obj == nil || obj.Exported() {
			continue
		}
		referrers, err := tx.symbolsReferencing(srcPkg, sym.Key())
		if err != nil {
			continue
		}
		for _, ref := range referrers {
			if movingKeys[objKey(tx.objectOf(ref.Sym))] {
				continue // also moving, not left behind
			}
			conflicts = append(conflicts, fmt.Sprintf(
				"%q still references unexported %q after it moves to %q",
				ref.Sym.Key(), sym.Key(), destPkg))
		}
	}

	return conflicts
}

// qualifierFixups computes the splices needed so every surviving reference
// across the moving/srcPkg boundary still resolves once moving relocates
// from srcPkg to destPkg, in both directions:
//   - Inbound: an external reference to an exported moving symbol. A
//     same-package (srcPkg) reference gains destPkg's qualifier, a
//     reference already qualified toward destPkg (the new home) loses its
//     qualifier, and one qualified toward any other package gets it
//     repointed.
//   - Outbound: a moving declaration's own reference to an exported
//     symbol staying behind in srcPkg (moveConflicts only refuses an
//     *unexported* one; an exported one isn't a conflict, but the
//     reference still needs to gain srcPkg's qualifier once the
//     referencing code itself relocates).
//
// A reference between two symbols that are both moving is left untouched
// either way — both land in destPkg together, unqualified is still
// correct on the other side. Only ever reached for a cross-package move —
// moveConflicts already refused every case this can't repair.
func (tx *Tx) qualifierFixups(moving []*workspace.Symbol, srcPkg, destPkg address.PkgPath) (map[address.RelativePath][]splice, error) {
	destOwner, ok := tx.resolvePackage(destPkg)
	if !ok {
		return nil, fmt.Errorf("no package at %q", destPkg)
	}
	srcOwner, ok := tx.resolvePackage(srcPkg)
	if !ok {
		return nil, fmt.Errorf("no package at %q", srcPkg)
	}

	type declSpan struct{ start, end token.Pos }
	movingSpans := make(map[address.RelativePath][]declSpan, len(moving))
	movingKeys := make(map[string]bool, len(moving))
	inboundTargets := make(map[string]bool, len(moving))
	for _, sym := range moving {
		movingSpans[sym.File] = append(movingSpans[sym.File], declSpan{sym.Decl().Pos(), sym.Decl().End()})
		obj := tx.objectOf(sym)
		if obj == nil {
			return nil, fmt.Errorf("type information unavailable for %q", sym.Key())
		}
		movingKeys[objKey(obj)] = true
		if obj.Exported() {
			inboundTargets[objKey(obj)] = true
		}
	}
	fromMoving := func(file address.RelativePath, pos token.Pos) bool {
		for _, sp := range movingSpans[file] {
			if pos >= sp.start && pos < sp.end {
				return true
			}
		}
		return false
	}

	edits := make(map[address.RelativePath][]splice)
	handle := func(pkg *workspace.Package, file *workspace.File, name *ast.Ident, qualifier ast.Expr) {
		obj := pkg.TypesInfo().Uses[name]
		if obj == nil {
			return
		}
		key := objKey(obj)
		moving := fromMoving(file.Path, name.Pos())
		switch {
		case inboundTargets[key] && !moving:
			if pkg.PkgPath == destPkg {
				if qualifier != nil {
					if sp, ok := tx.offsetSpan(file.Path, qualifier.Pos(), name.End()); ok {
						edits[file.Path] = append(edits[file.Path], splice{span: sp, repl: []byte(name.Name)})
					}
				}
			} else if qualifier != nil {
				if sp, ok := tx.offsetSpan(file.Path, qualifier.Pos(), qualifier.End()); ok {
					edits[file.Path] = append(edits[file.Path], splice{span: sp, repl: []byte(destOwner.Name)})
				}
			} else if sp, ok := tx.offsetSpan(file.Path, name.Pos(), name.End()); ok {
				edits[file.Path] = append(edits[file.Path], splice{span: sp, repl: []byte(destOwner.Name + "." + name.Name)})
			}
		case moving && qualifier == nil && obj.Pkg() != nil && obj.Pkg().Path() == string(srcPkg) &&
			obj.Exported() && !movingKeys[key]:
			if sp, ok := tx.offsetSpan(file.Path, name.Pos(), name.End()); ok {
				edits[file.Path] = append(edits[file.Path], splice{span: sp, repl: []byte(srcOwner.Name + "." + name.Name)})
			}
		}
	}

	for _, pkg := range tx.allPackages() {
		if pkg.TypesInfo() == nil {
			continue
		}
		for _, file := range pkg.Files() {
			ast.Inspect(file.Ast(), func(n ast.Node) bool {
				if sel, isSel := n.(*ast.SelectorExpr); isSel {
					handle(pkg, file, sel.Sel, sel.X)
					return false
				}
				if ident, isIdent := n.(*ast.Ident); isIdent {
					handle(pkg, file, ident, nil)
				}
				return true
			})
		}
	}
	return edits, nil
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
