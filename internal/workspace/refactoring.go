package workspace

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strconv"
	"strings"
)

// Splice is one byte-span edit against a file's canonical Src: replace
// [Start, End) with Repl. A Value Object — safe to return across the
// Aggregate boundary, unlike the Symbol/Package/File pointers the
// analysis that produces one is computed from.
type Splice struct {
	Path       FilePath
	Start, End int
	Repl       []byte
}

// span is a byte-offset range [start, end) into a file's canonical Src —
// the internal coordinate offsetSpan produces, immediately turned into
// either extracted text or a Splice. store no longer keeps its own
// copy: View.offsetSpan/Tx.leadingDocWord return the raw (start, end
// int) pair directly, and every mutation-facing edit is a Splice.
type span struct{ start, end int }

// keyOf computes obj's ObjectKey, or ok=false when obj carries no
// resolvable identity (nil, or missing its package).
func keyOf(obj types.Object) (ObjectKey, bool) {
	if obj == nil || obj.Pkg() == nil {
		return "", false
	}
	name := obj.Name()
	if fn, ok := obj.(*types.Func); ok {
		if recv := fn.Signature().Recv(); recv != nil {
			if recvName := recvNameOfType(recv.Type()); recvName != "" {
				name = recvName + "." + name
			}
		}
	}
	return ObjectKey(obj.Pkg().Path() + ":" + name), true
}

// recvNameOfType unwraps a receiver's types.Type down to its base type
// name — the semantic sibling of RecvTypeName, which does the same on the
// AST.
func recvNameOfType(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

// ProdPackage resolves a workspace address to its production package.
func (w *Workspace) ProdPackage(pkg PackagePath) (*Package, bool) {
	unit, ok := w.Unit(pkg)
	if !ok || unit.Prod() == nil {
		return nil, false
	}
	return unit.Prod(), true
}

// ResolveSymbol resolves a package address and symbol key ("Name" or
// "Recv.Name") to the symbol and its owning package, checking Prod before
// XTest before falling back to the external dependency cache — the one
// resolver every address-based lookup in this package composes on, so
// dependency symbols work everywhere a workspace symbol does.
func (w *Workspace) ResolveSymbol(pkg PackagePath, key string) (*Symbol, *Package, bool) {
	if unit, ok := w.Unit(pkg); ok {
		for _, p := range unit.Members() {
			if sym, ok := p.Symbol(key); ok {
				return sym, p, true
			}
		}
	}
	if p, ok := w.LookupExternal(pkg); ok {
		if sym, ok := p.Symbol(key); ok {
			return sym, p, true
		}
	}
	return nil, nil, false
}

// allPackages enumerates every workspace package (never the external
// dependency cache), Prod before XTest per unit, in address order.
func (w *Workspace) allPackages() []*Package {
	var out []*Package
	for _, pkg := range w.UnitKeys() {
		unit, _ := w.Unit(pkg)
		if prod := unit.Prod(); prod != nil {
			out = append(out, prod)
		}
		if xtest := unit.XTest(); xtest != nil {
			out = append(out, xtest)
		}
	}
	return out
}

// referencesTo finds every symbol, across every workspace package, whose
// declaration resolves a reference to target (an ObjectKey identity) —
// excluding exclude and any symbol already collected (self-references,
// recursion). Only matches package-level declarations and methods — see
// isPackageLevelUse's doc comment for why that guard matters. The same
// identity-not-pointer matching SymbolsReferencing (scanning.go) uses,
// here scoped to one call site's own DetectMoveConflicts check instead of
// a public, address-resolved scan.
func (w *Workspace) referencesTo(target ObjectKey, exclude *Symbol) []symbolRef {
	seen := make(map[*Symbol]bool)
	var out []symbolRef
	for _, p := range w.allPackages() {
		if p.TypesInfo() == nil {
			continue
		}
		for ident, obj := range p.TypesInfo().Uses {
			if !isPackageLevelUse(obj) {
				continue
			}
			key, ok := keyOf(obj)
			if !ok || key != target {
				continue
			}
			encl, ok := p.symbolAt(ident.Pos())
			if !ok || encl == exclude || seen[encl] {
				continue
			}
			seen[encl] = true
			out = append(out, symbolRef{Pkg: p, Sym: encl})
		}
	}
	return out
}

// DetectMoveConflicts reports every reason relocating movingKeys (all currently
// declared in srcPkg) to destPkg would break something — nil means the
// move is safe. Same-package moves are always safe: nothing about
// visibility or receiver locality changes when the package doesn't.
// Aggregate-owned business rule: pure analysis over data the model
// already holds, resolved fresh from movingKeys here rather than accepted
// as pointers a caller might have resolved earlier and be holding stale —
// the point of hosting this on Workspace rather than a client is that the
// pointers this touches never need to survive past this one call.
//
// Checked in order, cheapest and most unconditional first:
//  1. Method receiver locality, both directions: a method's receiver type
//     must be declared in the same package as the method — not a
//     visibility question, just illegal Go otherwise. A moving method
//     needs its receiver type moving too; symmetrically, a method staying
//     behind needs its receiver type staying too. movingNames tracks only
//     moving *types* (never methods, funcs, vars, or consts): a receiver
//     can only ever be a type, and including other kinds' bare names
//     risks a false match against an unrelated receiver of the same name
//     (a method literally named the same as some other moving type's
//     bare name, e.g. a method "File" on type "Symbol" colliding with an
//     unrelated type also named "File" — a real bug this once was).
//  2. Collision: does destPkg already declare something by this name?
//  3. Dependency: does a moving declaration reference an unexported
//     package-level sibling staying behind in srcPkg? (Local variables
//     and parameters are unexported too by convention, but aren't
//     workspace symbols and travel with the declaration regardless —
//     excluded by requiring the object's parent scope to be the package
//     scope itself, not some inner function/block scope.)
//  4. Blocking referrer: does code staying behind in srcPkg reference an
//     unexported symbol that's leaving? (An exported one leaving is a
//     fixup, not a conflict — see ComputeQualifierFixups.)
func (w *Workspace) DetectMoveConflicts(srcPkg, destPkg PackagePath, movingKeys []string) []string {
	if srcPkg == destPkg {
		return nil
	}
	type resolved struct {
		sym   *Symbol
		owner *Package
	}
	moving := make([]resolved, 0, len(movingKeys))
	for _, key := range movingKeys {
		if sym, owner, ok := w.ResolveSymbol(srcPkg, key); ok {
			moving = append(moving, resolved{sym, owner})
		}
	}
	movingObjKeys := make(map[ObjectKey]bool, len(moving))
	movingNames := make(map[string]bool, len(moving))
	for _, m := range moving {
		if k, ok := keyOf(m.owner.objectOf(m.sym)); ok {
			movingObjKeys[k] = true
		}
		if m.sym.Kind == KindType {
			movingNames[m.sym.Name] = true
		}
	}

	var conflicts []string
	destOwner, destExists := w.ProdPackage(destPkg)
	for _, m := range moving {
		sym := m.sym
		if sym.Kind == KindMethod && !movingNames[sym.Recv] {
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

	srcOwner, ok := w.ProdPackage(srcPkg)
	if ok {
		for _, s := range srcOwner.Symbols() {
			k, _ := keyOf(srcOwner.objectOf(s))
			if s.Kind != KindMethod || movingObjKeys[k] {
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
		for _, m := range moving {
			sym := m.sym
			ast.Inspect(sym.Decl(), func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				obj := srcOwner.TypesInfo().Uses[ident]
				if obj == nil || obj.Pkg() == nil || obj.Parent() != pkgScope {
					return true
				}
				if obj.Pkg().Path() == string(srcPkg) && !obj.Exported() {
					k, _ := keyOf(obj)
					if !movingObjKeys[k] {
						conflicts = append(conflicts, fmt.Sprintf(
							"%q depends on unexported %q, which stays in %q",
							sym.Key(), obj.Name(), srcPkg))
					}
				}
				return true
			})
		}
	}

	for _, m := range moving {
		sym, owner := m.sym, m.owner
		obj := owner.objectOf(sym)
		if obj == nil || obj.Exported() {
			continue
		}
		target, ok := keyOf(obj)
		if !ok {
			continue
		}
		for _, ref := range w.referencesTo(target, sym) {
			k, _ := keyOf(ref.Pkg.objectOf(ref.Sym))
			if movingObjKeys[k] {
				continue // also moving, not left behind
			}
			conflicts = append(conflicts, fmt.Sprintf(
				"%q still references unexported %q after it moves to %q",
				ref.Sym.Key(), sym.Key(), destPkg))
		}
	}

	return conflicts
}

// ComputeQualifierFixups computes the splices needed so every surviving reference
// across the moving/srcPkg boundary still resolves once moving relocates
// from srcPkg to destPkg, in both directions:
//   - Inbound: an external reference to an exported moving symbol. A
//     same-package (srcPkg) reference gains destPkg's qualifier, a
//     reference already qualified toward destPkg (the new home) loses its
//     qualifier, and one qualified toward any other package gets it
//     repointed.
//   - Outbound: a moving declaration's own reference to an exported
//     symbol staying behind in srcPkg (DetectMoveConflicts only refuses an
//     *unexported* one; an exported one isn't a conflict, but the
//     reference still needs to gain srcPkg's qualifier once the
//     referencing code itself relocates).
//
// A reference between two symbols that are both moving is left untouched
// either way — both land in destPkg together, unqualified is still
// correct on the other side. Only ever reached for a cross-package move —
// DetectMoveConflicts already refused every case this can't repair.
//
// Only a genuine package-level declaration (type, func, var, const) is
// ever a qualifier-fixup target — never a method: a method call's own
// receiver expression (`x` in `x.Method()`) is a value, not a package
// qualifier, so the call site needs no rewriting no matter which package
// the method's receiver type lives in. Only the receiver type's own bare
// references (a var declaration, a composite literal, ...) carry a
// qualifier to fix up, and those reach this function as plain
// identifiers, not through a moving method's call sites.
//
// movingKeys is resolved to symbols here, fresh, matching
// DetectMoveConflicts — see its doc comment for why. Parameter order
// matches DetectMoveConflicts' (srcPkg, destPkg, movingKeys), not the
// reverse — the two are always called back-to-back on the same
// relocation.
func (w *Workspace) ComputeQualifierFixups(srcPkg, destPkg PackagePath, movingKeys []string) ([]Splice, error) {
	destOwner, ok := w.ProdPackage(destPkg)
	if !ok {
		return nil, NoPackageError(destPkg)
	}
	srcOwner, ok := w.ProdPackage(srcPkg)
	if !ok {
		return nil, NoPackageError(srcPkg)
	}
	type resolved struct {
		sym   *Symbol
		owner *Package
	}
	moving := make([]resolved, 0, len(movingKeys))
	for _, key := range movingKeys {
		if sym, owner, ok := w.ResolveSymbol(srcPkg, key); ok {
			moving = append(moving, resolved{sym, owner})
		}
	}

	type declSpan struct{ start, end token.Pos }
	movingSpans := make(map[FilePath][]declSpan, len(moving))
	movingObjKeys := make(map[ObjectKey]bool, len(moving))
	inboundTargets := make(map[ObjectKey]bool, len(moving))
	for _, m := range moving {
		sym, owner := m.sym, m.owner
		movingSpans[sym.File] = append(movingSpans[sym.File], declSpan{sym.Decl().Pos(), sym.Decl().End()})
		obj := owner.objectOf(sym)
		if obj == nil {
			return nil, errNoTypeInfo(sym.Key())
		}
		k, ok := keyOf(obj)
		if !ok {
			return nil, errNoTypeInfo(sym.Key())
		}
		movingObjKeys[k] = true
		if obj.Exported() {
			inboundTargets[k] = true
		}
	}
	fromMoving := func(file FilePath, pos token.Pos) bool {
		for _, sp := range movingSpans[file] {
			if pos >= sp.start && pos < sp.end {
				return true
			}
		}
		return false
	}

	var edits []Splice
	handle := func(pkg *Package, file *File, name *ast.Ident, qualifier ast.Expr) {
		obj := pkg.TypesInfo().Uses[name]
		if obj == nil || obj.Pkg() == nil || obj.Parent() != obj.Pkg().Scope() {
			return
		}
		key, _ := keyOf(obj)
		moving := fromMoving(file.Path, name.Pos())
		switch {
		case inboundTargets[key] && !moving:
			if pkg.ID.Base() == destPkg {
				if qualifier != nil {
					if sp, ok := w.NewSpliceAtPos(pkg, file.Path, qualifier.Pos(), name.End(), []byte(name.Name)); ok {
						edits = append(edits, sp)
					}
				}
			} else if qualifier != nil {
				if sp, ok := w.NewSpliceAtPos(pkg, file.Path, qualifier.Pos(), qualifier.End(), []byte(destOwner.Name)); ok {
					edits = append(edits, sp)
				}
			} else if sp, ok := w.NewSpliceAtPos(pkg, file.Path, name.Pos(), name.End(), []byte(destOwner.Name+"."+name.Name)); ok {
				edits = append(edits, sp)
			}
		case moving && qualifier == nil && obj.Pkg() != nil && obj.Pkg().Path() == string(srcPkg) &&
			obj.Exported() && !movingObjKeys[key]:
			if sp, ok := w.NewSpliceAtPos(pkg, file.Path, name.Pos(), name.End(), []byte(srcOwner.Name+"."+name.Name)); ok {
				edits = append(edits, sp)
			}
		}
	}

	for _, pkg := range w.allPackages() {
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

// ComputeRenameSplices computes the splices renaming every resolved reference to
// pkg's key (matched by qualified name, not pointer identity — a
// declaration's plain and test-expanded variants yield distinct
// *types.Func instances for the same object) to newName, across the
// whole workspace. Does not touch the declaration's own defining
// identifier or its doc comment — the caller's job, since both need the
// resolved Symbol itself, which the caller already has in hand before
// reaching this. Only matches package-level declarations and methods —
// see isPackageLevelUse's doc comment for why that guard matters.
// Aggregate-owned analysis, same rationale as DetectMoveConflicts: key is
// resolved fresh here, not accepted as a pointer a caller might already
// be holding.
func (w *Workspace) ComputeRenameSplices(pkg PackagePath, key, newName string) ([]Splice, error) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return nil, NoSymbolError(key, pkg)
	}
	target, ok := keyOf(owner.objectOf(sym))
	if !ok {
		return nil, errNoTypeInfo(key)
	}
	var edits []Splice
	for _, p := range w.allPackages() {
		if p.TypesInfo() == nil {
			continue
		}
		for ident, obj := range p.TypesInfo().Uses {
			if !isPackageLevelUse(obj) {
				continue
			}
			k, ok := keyOf(obj)
			if !ok || k != target {
				continue
			}
			file, ok := p.fileContaining(ident.Pos())
			if !ok {
				continue
			}
			if sp, ok := w.NewSpliceAtPos(p, file.Path, ident.Pos(), ident.End(), []byte(newName)); ok {
				edits = append(edits, sp)
			}
		}
	}
	return edits, nil
}

// ComputePackageMoveSplices computes the import-path and (when renameName)
// qualifier-rename splices every importer of oldPkg needs once it moves
// to newPkg — oldBase/newBase are the package's bare name before and
// after, only consulted when renameName is true (the package's name
// equalled its address base, the convention, and the move changes that
// base). The moving package's own production half is never a target
// (its files are disjoint from what needs to change); its XTest half is,
// since it imports its own Prod sibling. Aggregate-owned analysis, same
// rationale as DetectMoveConflicts.
func (w *Workspace) ComputePackageMoveSplices(oldPkg, newPkg PackagePath, renameName bool, oldBase, newBase string) []Splice {
	prodOwner, _ := w.ProdPackage(oldPkg)
	oldImport, newImport := string(oldPkg), string(newPkg)
	var edits []Splice
	for _, pkg := range w.allPackages() {
		if pkg == prodOwner {
			continue
		}
		for _, file := range pkg.Files() {
			for _, imp := range file.Ast().Imports {
				if imp.Path.Value != strconv.Quote(oldImport) {
					continue
				}
				if sp, ok := w.NewSpliceAtPos(pkg, file.Path, imp.Path.Pos(), imp.Path.End(), []byte(strconv.Quote(newImport))); ok {
					edits = append(edits, sp)
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
				file, ok := pkg.fileContaining(ident.Pos())
				if !ok {
					continue
				}
				if sp, ok := w.NewSpliceAtPos(pkg, file.Path, ident.Pos(), ident.End(), []byte(newBase)); ok {
					edits = append(edits, sp)
				}
			}
		}
	}
	return edits
}

// ValidateNewName validates a rename's destination key against the
// symbol actually being renamed: a method's receiver can never change
// through a rename, so newKey must name it explicitly ("Recv.Name") and
// Recv must match; a non-method's newKey must be a bare identifier,
// since there is no receiver to qualify it with. Also refuses an
// exported→unexported rename with external references still standing —
// see refuseUnsafeUnexport.
func (w *Workspace) ValidateNewName(pkg PackagePath, key, newKey string) (newName string, err error) {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
	if !ok {
		return "", NoSymbolError(key, pkg)
	}
	name := newKey
	if sym.Kind != KindMethod {
		if strings.Contains(newKey, ".") {
			return "", fmt.Errorf("%q is not a method: newSymbolKey must be a bare identifier", sym.Key())
		}
	} else {
		recv, methodName, ok := strings.Cut(newKey, ".")
		if !ok {
			return "", fmt.Errorf("%q is a method: newSymbolKey must be %q (its receiver cannot change)", sym.Key(), sym.Recv+".<new name>")
		}
		if recv != sym.Recv {
			return "", fmt.Errorf("cannot change %q's receiver: got %q, want %q", sym.Key(), recv, sym.Recv)
		}
		name = methodName
	}
	if err := w.refuseUnsafeUnexport(owner, sym, name); err != nil {
		return "", err
	}
	return name, nil
}

// offsetSpan converts a position range into byte offsets in file's Src —
// the primitive under QualifierFixups' splice computation. Valid because
// Ast is by invariant a parse of exactly Src.
func (w *Workspace) offsetSpan(owner *Package, file *File, from, to token.Pos) (span, bool) {
	if !from.IsValid() || !to.IsValid() {
		return span{}, false
	}
	fset := w.FsetOf(owner)
	start := fset.Position(from).Offset
	end := fset.Position(to).Offset
	if start < 0 || end > len(file.Src()) || start > end {
		return span{}, false
	}
	return span{start: start, end: end}, true
}

// NewSpliceAtPos narrows a token.Pos range within pkg's path into a Splice
// replacing that range with repl, resolving it through NewSpliceAtOffset —
// ok=false when the range doesn't resolve to a valid, in-bounds byte
// span.
func (w *Workspace) NewSpliceAtPos(pkg *Package, path FilePath, from, to token.Pos, repl []byte) (Splice, bool) {
	file, ok := pkg.File(path)
	if !ok {
		return Splice{}, false
	}
	sp, ok := w.offsetSpan(pkg, file, from, to)
	if !ok {
		return Splice{}, false
	}
	return w.NewSpliceAtOffset(pkg, path, sp.start, sp.end, repl)
}

// NewSpliceAtOffset validates a pre-resolved byte range [start, end) against
// pkg's path and returns the Splice replacing it with repl — ok=false
// when the range isn't 0 <= start <= end <= len(src). The one place a
// byte range is checked before becoming an edit: NewSpliceAtPos funnels
// through this once it has resolved a token.Pos range to offsets, and a
// caller that already holds a resolved offset (an insertion point from
// InsertOffset, an extraction span from ExtractDeclaration) uses it
// directly instead of hand-building a Splice.
func (w *Workspace) NewSpliceAtOffset(pkg *Package, path FilePath, start, end int, repl []byte) (Splice, bool) {
	file, ok := pkg.File(path)
	if !ok {
		return Splice{}, false
	}
	if start < 0 || end > len(file.Src()) || start > end {
		return Splice{}, false
	}
	return Splice{Path: path, Start: start, End: end, Repl: repl}, true
}

// ApplyFileSplices groups splices by file and installs each file's result
// via SwapFile (parsed, goimports-formatted), deduplicating overlapping
// gathers. Returns every path written, in address order — a caller's
// material for its own change-tracking, since tracking what changed isn't
// this method's own concern.
func (w *Workspace) ApplyFileSplices(splices []Splice) ([]FilePath, error) {
	byPath := make(map[FilePath][]Splice)
	for _, s := range splices {
		byPath[s.Path] = append(byPath[s.Path], s)
	}
	paths := make([]FilePath, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	touched := make([]FilePath, 0, len(paths))
	for _, path := range paths {
		file, owner, ok := w.ResolveFileByPath(path)
		if !ok {
			return nil, fmt.Errorf("cannot resolve %q while applying splices", path)
		}
		batch := byPath[path]
		slices.SortFunc(batch, func(a, b Splice) int { return cmp.Compare(a.Start, b.Start) })
		batch = slices.CompactFunc(batch, func(a, b Splice) bool { return a.Start == b.Start && a.End == b.End })
		addr := path.PackagePath()
		if err := w.SwapFile(addr, owner.ID.Kind() == KindXTest, path, ApplySplices(file.Src(), batch)); err != nil {
			return nil, err
		}
		touched = append(touched, path)
	}
	return touched, nil
}

// RelocateDeclaration extracts key's own declaration from srcPkg and
// splices it into destPath (already confirmed to belong to destPkg,
// created if missing) — the mechanical half of a relocation, with no
// safety checks of its own beyond the two structural guards every
// relocation needs regardless of scope (already-there, test-boundary
// crossing): callers are responsible for DetectMoveConflicts and
// ComputeQualifierFixups/ApplyFileSplices first, and only once, up
// front — not here, since a batch relocates several keys one at a time
// and an already-relocated key no longer resolves from srcPkg, so a
// conflict check repeated mid-batch would incorrectly see it as left
// behind. Everything this needs — the symbol's own kind and receiver,
// destPath's file, the extracted text — is resolved fresh from w, once,
// inside this one call: no caller ever holds a pointer across it.
// Returns every path written, for the caller's own change-tracking.
func (w *Workspace) RelocateDeclaration(srcPkg, destPkg PackagePath, key string, destPath FilePath) ([]FilePath, error) {
	sym, owner, ok := w.ResolveSymbol(srcPkg, key)
	if !ok {
		return nil, NoSymbolError(key, srcPkg)
	}
	destOwner, ok := w.ProdPackage(destPkg)
	if !ok {
		return nil, NoPackageError(destPkg)
	}
	if destOwner == owner && destPath == sym.File {
		return nil, fmt.Errorf("%q already lives in %q", key, destPath)
	}
	if strings.HasSuffix(destPath.String(), "_test.go") != strings.HasSuffix(sym.File.String(), "_test.go") {
		return nil, fmt.Errorf("moving %q from %q to %q would cross the test build boundary", key, sym.File, destPath)
	}
	srcIsXTest := owner.ID.Kind() == KindXTest
	kind, recv := sym.Kind, sym.Recv
	src, extractSplice, err := w.ExtractDeclaration(srcPkg, key)
	if err != nil {
		return nil, err
	}
	dest, inDest := destOwner.File(destPath)
	if _, _, exists := w.ResolveFileByPath(destPath); exists && !inDest {
		return nil, fmt.Errorf("file %q belongs to another package", destPath)
	}
	file, _ := owner.File(sym.File)
	if err := w.SwapFile(srcPkg, srcIsXTest, sym.File, ApplySplices(file.Src(), []Splice{extractSplice})); err != nil {
		return nil, err
	}
	if !inDest {
		if err := w.SwapFile(destPkg, false, destPath, []byte("package "+destOwner.Name+"\n\n"+src+"\n")); err != nil {
			return nil, err
		}
		return []FilePath{sym.File, destPath}, nil
	}
	dest, destOwner, ok = w.ResolveFileByPath(destPath)
	if !ok {
		return nil, VanishedError(destPath, "after relocation")
	}
	at, ok := w.InsertOffset(destPkg, destPath, kind, recv)
	if !ok {
		return nil, NoInsertionPointError(destPath)
	}
	sp, ok := w.NewSpliceAtOffset(destOwner, destPath, at, at, []byte("\n\n"+src+"\n"))
	if !ok {
		return nil, NoInsertionPointError(destPath)
	}
	if err := w.SwapFile(destPkg, false, destPath, ApplySplices(dest.Src(), []Splice{sp})); err != nil {
		return nil, err
	}
	return []FilePath{sym.File, destPath}, nil
}

// RelocateFile moves path from pkg's isXTest half to newPath in
// newPkgPath (already resolved, already confirmed not to exist) — the
// cross-package half of MoveFile, refused when DetectMoveConflicts can
// prove in advance it would break the workspace. Every surviving
// reference across the move boundary is fixed up first
// (ComputeQualifierFixups/ApplyFileSplices) — external callers of the
// file's exported declarations, and the file's own outbound references
// to exported siblings staying behind, alike — with pkg's and
// newPkgPath's packages re-resolved fresh afterward, since applying
// those fixups may have forked either. Returns every path written, for
// the caller's own change-tracking.
func (w *Workspace) RelocateFile(pkg PackagePath, path FilePath, isXTest bool, newPkgPath PackagePath, newPath FilePath) ([]FilePath, error) {
	unit, ok := w.Unit(pkg)
	if !ok {
		return nil, NoPackageError(pkg)
	}
	owner := unit.Prod()
	if isXTest {
		owner = unit.XTest()
	}
	destOwner, ok := w.ProdPackage(newPkgPath)
	if !ok {
		return nil, NoPackageError(newPkgPath)
	}

	var movingKeys []string
	for _, sym := range owner.Symbols() {
		if sym.File == path {
			movingKeys = append(movingKeys, sym.Key())
		}
	}
	if conflicts := w.DetectMoveConflicts(pkg, newPkgPath, movingKeys); len(conflicts) > 0 {
		return nil, fmt.Errorf("moving %q to %q would break the workspace: %s", path, newPkgPath, strings.Join(conflicts, "; "))
	}
	fixups, err := w.ComputeQualifierFixups(pkg, newPkgPath, movingKeys)
	if err != nil {
		return nil, err
	}
	var touched []FilePath
	if len(fixups) > 0 {
		fixupTouched, err := w.ApplyFileSplices(fixups)
		if err != nil {
			return nil, err
		}
		touched = append(touched, fixupTouched...)
		// ApplyFileSplices may have forked owner's or destOwner's package if
		// either had a file among the fixups — re-resolve both from their
		// stable addresses rather than trust the pointers captured above.
		unit, ok = w.Unit(pkg)
		if !ok {
			return nil, VanishedError(pkg, "after qualifier fixups")
		}
		owner = unit.Prod()
		if isXTest {
			owner = unit.XTest()
		}
		if owner == nil {
			return nil, VanishedError(pkg, "after qualifier fixups")
		}
		destOwner, ok = w.ProdPackage(newPkgPath)
		if !ok {
			return nil, VanishedError(newPkgPath, "after qualifier fixups")
		}
	}
	file, _ := owner.File(path)
	candidate := file.Src()
	if sp, ok := w.NewSpliceAtPos(owner, path, file.Ast().Name.Pos(), file.Ast().Name.End(), []byte(destOwner.Name)); ok {
		candidate = ApplySplices(candidate, []Splice{sp})
	}
	w.DropFile(pkg, isXTest, path)
	touched = append(touched, path)
	if err := w.SwapFile(newPkgPath, false, newPath, candidate); err != nil {
		return nil, err
	}
	touched = append(touched, newPath)
	return touched, nil
}

// MovePackage moves oldPkg to newPkg — rewriting the import path in every
// importer, and (when renameName) the package clause, every unaliased
// qualifier, and each file's own "Package oldBase" doc-comment opening.
// oldPkg and its Unit are re-resolved fresh after applying the
// import-rewrite splices, since XTest imports its own Prod sibling and
// so can itself be a splice target. Both halves' shells are built before
// NewUnit assembles them atomically — there is no point where a
// half-built Unit could be installed or observed, since NewUnit is the
// only way to construct one at all. Returns every path written, for the
// caller's own change-tracking.
func (w *Workspace) MovePackage(oldPkg, newPkg PackagePath, renameName bool, oldBase, newBase string) ([]FilePath, error) {
	unit, ok := w.Unit(oldPkg)
	if !ok {
		return nil, NoPackageError(oldPkg)
	}
	if _, exists := w.Unit(newPkg); exists {
		return nil, PackageExistsError(newPkg)
	}

	edits := w.ComputePackageMoveSplices(oldPkg, newPkg, renameName, oldBase, newBase)
	touched, err := w.ApplyFileSplices(edits)
	if err != nil {
		return nil, err
	}
	// ApplyFileSplices may have forked unit.XTest's package (it imports
	// its own Prod sibling, so it's a splice target) — re-resolve the
	// unit fresh rather than trust the pointer captured before the splice.
	unit, ok = w.Unit(oldPkg)
	if !ok {
		return nil, VanishedError(oldPkg, "after import rewrites")
	}

	type half struct {
		orig, moved *Package
		isXTest     bool
	}
	var halves []half
	var prodMoved, xtestMoved *Package
	for _, orig := range unit.Members() {
		moved := orig.Relocated(newPkg, renameName)
		isXTest := orig.ID.Kind() == KindXTest
		halves = append(halves, half{orig: orig, moved: moved, isXTest: isXTest})
		if isXTest {
			xtestMoved = moved
		} else {
			prodMoved = moved
		}
	}
	w.InstallUnit(newPkg, NewUnit(prodMoved, xtestMoved))
	for _, h := range halves {
		for _, file := range h.orig.Files() {
			newPath := newPkg.File(file.Path.Base())
			w.Tombstone(oldPkg, file.Path, h.orig.Name)
			w.ClearTombstone(newPath)
			touched = append(touched, file.Path, newPath)
			candidate := file.Src()
			if renameName {
				var fileSplices []Splice
				if sp, ok := w.NewSpliceAtPos(h.orig, file.Path, file.Ast().Name.Pos(), file.Ast().Name.End(), []byte(h.moved.Name)); ok {
					fileSplices = append(fileSplices, sp)
				} else {
					return nil, fmt.Errorf("cannot locate package clause of %q", file.Path)
				}
				if from, to, ok := LeadingDocWord(file.Ast().Doc, "Package ", oldBase); ok {
					if sp, ok := w.NewSpliceAtPos(h.orig, file.Path, from, to, []byte(newBase)); ok {
						fileSplices = append(fileSplices, sp)
					}
				}
				candidate = ApplySplices(file.Src(), fileSplices)
			}
			if err := w.SwapFile(newPkg, h.isXTest, newPath, candidate); err != nil {
				return nil, err
			}
		}
	}
	w.RemoveUnit(oldPkg)
	return touched, nil
}

// RelocateSymbols relocates every key in keys (plus each key's own
// position-dependent group members) from srcPkg to a file at destPath
// (already confirmed to belong to destPkg) — the one door for a symbol
// relocation, whether one key or many. DetectMoveConflicts and
// ComputeQualifierFixups/ApplyFileSplices run exactly once, against the
// whole moving set, before anything is relocated — never per-key, since
// an already-relocated key no longer resolves from srcPkg, and a
// conflict check repeated mid-batch would incorrectly see a sibling
// still waiting its turn as left behind. Types are placed before their
// own methods regardless of input order, so InsertOffset's "attach to
// your receiver" placement resolves correctly for a method landing
// right after the type it just moved in with. Returns every path
// written.
func (w *Workspace) RelocateSymbols(srcPkg, destPkg PackagePath, keys []string, destPath FilePath) ([]FilePath, error) {
	seen := make(map[string]bool, len(keys))
	var movingKeys []string
	for _, key := range keys {
		members, err := w.PositionDependentGroupMembers(srcPkg, key)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if !seen[m] {
				seen[m] = true
				movingKeys = append(movingKeys, m)
			}
		}
	}
	if conflicts := w.DetectMoveConflicts(srcPkg, destPkg, movingKeys); len(conflicts) > 0 {
		return nil, fmt.Errorf("moving %v to %q would break the workspace: %s", keys, destPkg, strings.Join(conflicts, "; "))
	}
	var touched []FilePath
	if srcPkg != destPkg {
		fixups, err := w.ComputeQualifierFixups(srcPkg, destPkg, movingKeys)
		if err != nil {
			return nil, err
		}
		if len(fixups) > 0 {
			fixupTouched, err := w.ApplyFileSplices(fixups)
			if err != nil {
				return nil, err
			}
			touched = append(touched, fixupTouched...)
		}
	}

	claimed := make(map[string]bool, len(movingKeys))
	var representatives []string
	for _, key := range movingKeys {
		if claimed[key] {
			continue
		}
		group, err := w.PositionDependentGroupMembers(srcPkg, key)
		if err != nil {
			return nil, err
		}
		for _, m := range group {
			claimed[m] = true
		}
		representatives = append(representatives, key)
	}

	ordered := make([]string, 0, len(representatives))
	for _, key := range representatives {
		if sym, _, ok := w.ResolveSymbol(srcPkg, key); ok && sym.Kind == KindType {
			ordered = append(ordered, key)
		}
	}
	for _, key := range representatives {
		if sym, _, ok := w.ResolveSymbol(srcPkg, key); ok && sym.Kind != KindType {
			ordered = append(ordered, key)
		}
	}
	for _, key := range ordered {
		keyTouched, err := w.RelocateDeclaration(srcPkg, destPkg, key, destPath)
		if err != nil {
			return nil, err
		}
		touched = append(touched, keyTouched...)
	}
	return touched, nil
}

// refuseUnsafeUnexport refuses a rename that would flip sym from exported
// to unexported while a reference from a different package still stands
// (Prod and XTest count as different packages here, same as everywhere
// else in this file). Once unexported, an external reference becomes a
// compile error go/types can never resolve again — not a recheck-scope
// gap, a real Go visibility rule — so ComputeRenameSplices has nothing
// left to find on a later revert: the reference (and, when it was the
// file's only reason to import the package, the import itself, silently
// dropped by goimports) is left permanently stale with no diagnostic
// pointing back at it. Same-package references are unaffected by
// exported-ness and stay safe.
func (w *Workspace) refuseUnsafeUnexport(owner *Package, sym *Symbol, newName string) error {
	if !token.IsExported(sym.Name) || token.IsExported(newName) {
		return nil
	}
	target, ok := keyOf(owner.objectOf(sym))
	if !ok {
		return nil
	}
	seen := make(map[*Package]bool)
	var pkgs []string
	for _, ref := range w.referencesTo(target, sym) {
		if ref.Pkg == owner || seen[ref.Pkg] {
			continue
		}
		seen[ref.Pkg] = true
		pkgs = append(pkgs, ref.Pkg.ID.String())
	}
	if len(pkgs) == 0 {
		return nil
	}
	slices.Sort(pkgs)
	return fmt.Errorf("%q is exported and referenced from %s: unexporting it would leave those references permanently unresolvable on any later revert — refusing the rename", sym.Key(), strings.Join(pkgs, ", "))
}

// symbolRef pairs a referencing symbol with its owning package — the
// Aggregate-internal shape referencesTo collects, mirroring dto.Match's
// shape on the other side of the boundary.
type symbolRef struct {
	Pkg *Package
	Sym *Symbol
}

// ObjectKey identifies a types.Object semantically as "importpath:Recv.Name".
// Pointer identity is deliberately avoided: the same declaration yields
// distinct object instances in a package's plain and test-expanded
// variants.
type ObjectKey string

// isPackageLevelUse reports whether obj (typically a TypesInfo.Uses
// entry) denotes a genuine package-level declaration or a method — never
// a struct field, local variable, or parameter. ObjectKey's (package path,
// name) identity can't safely distinguish those from an unrelated
// top-level declaration sharing that bare name in the same package (a
// struct field colliding with a moving type of the same name is the
// case that actually broke this once); Parent() is the package scope for
// ordinary top-level declarations, and nil for a method's receiver-bound
// Func alongside fields/locals/params — methods are told apart by having
// a receiver, which is what ObjectKey's own compound key already keys on.
func isPackageLevelUse(obj types.Object) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	if obj.Parent() == obj.Pkg().Scope() {
		return true
	}
	fn, ok := obj.(*types.Func)
	return ok && fn.Signature().Recv() != nil
}

// ApplySplices applies every splice to src in descending offset order so
// earlier spans stay valid — workspace's own mutation primitive for byte
// content, the counterpart to its ComputeXSplices family of pure plans.
// Path is not consulted: every splice in one call is assumed to target
// the same src: a caller spanning several files groups by Path first.
func ApplySplices(src []byte, splices []Splice) []byte {
	slices.SortFunc(splices, func(a, b Splice) int { return cmp.Compare(b.Start, a.Start) })
	out := slices.Clone(src)
	for _, s := range splices {
		out = slices.Concat(out[:s.Start], s.Repl, out[s.End:])
	}
	return out
}

// LeadingDocWord locates "prefix"+"want" at the very start of doc's first
// line (right after the "// " comment marker) and returns the token.Pos
// span of just the "want" text, ok=false when the comment doesn't open with
// exactly that shape. This is what makes a doc-comment rewrite safe to
// automate: it only ever matches Go's own doc-comment conventions (a
// symbol's doc opens with its bare name; a package's doc opens with
// "Package name"), never prose that merely happens to mention the same
// word. Pure AST work — callers turn the returned span into a Splice via
// NewSpliceAtPos.
func LeadingDocWord(doc *ast.CommentGroup, prefix, want string) (from, to token.Pos, ok bool) {
	if doc == nil || len(doc.List) == 0 {
		return 0, 0, false
	}
	first := doc.List[0]
	body := strings.TrimPrefix(strings.TrimPrefix(first.Text, "//"), " ")
	bodyOffset := len(first.Text) - len(body)
	if !strings.HasPrefix(body, prefix+want) {
		return 0, 0, false
	}
	if rest := body[len(prefix+want):]; rest != "" {
		switch rest[0] {
		case ' ', '.', ',', ':', '\'':
		default:
			return 0, 0, false
		}
	}
	from = first.Pos() + token.Pos(bodyOffset+len(prefix))
	to = from + token.Pos(len(want))
	return from, to, true
}
