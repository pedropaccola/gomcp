package engine

import (
	"cmp"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// span is a byte-offset range [start, end) into a file's canonical Src.
type span struct{ start, end int }

// objKey identifies a types.Object semantically as "importpath:Recv.Name".
// Pointer identity is deliberately avoided: the same declaration yields
// distinct object instances in a package's plain and test-expanded variants.
func objKey(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return "" // universe scope or builtin: never a workspace symbol
	}
	name := obj.Name()
	if fn, ok := obj.(*types.Func); ok {
		if recv := fn.Signature().Recv(); recv != nil {
			if recvName := recvNameOfType(recv.Type()); recvName != "" {
				name = recvName + "." + name
			}
		}
	}
	return obj.Pkg().Path() + ":" + name
}

// objectOf resolves a symbol to its types.Object via the owning package's
// Defs map; nil when type information is unavailable.
func (v *View) objectOf(sym *workspace.Symbol) types.Object {
	_, owner, ok := v.resolveFile(sym.File)
	if !ok || owner.TypesInfo() == nil {
		return nil
	}
	ident := definingIdent(sym)
	if ident == nil {
		return nil
	}
	return owner.TypesInfo().Defs[ident]
}

// offsetSpan converts a position range into byte offsets in the file's Src —
// the primitive under both source extraction and mutation splicing. Valid
// because Ast is by invariant a parse of exactly Src. Positions resolve in
// the owner's FileSet, so dependency files extract like workspace ones.
func (v *View) offsetSpan(path address.RelativePath, from, to token.Pos) (span, bool) {
	file, owner, ok := v.resolveFile(path)
	if !ok || !from.IsValid() || !to.IsValid() {
		return span{}, false
	}
	fset := v.fsetOf(owner)
	start := fset.Position(from).Offset
	end := fset.Position(to).Offset
	if start < 0 || end > len(file.Src()) || start > end {
		return span{}, false
	}
	return span{start: start, end: end}, true
}

// definingIdent returns the identifier that declares the symbol.
func definingIdent(sym *workspace.Symbol) *ast.Ident {
	if fn, ok := sym.Decl().(*ast.FuncDecl); ok {
		return fn.Name
	}
	switch spec := sym.Spec().(type) {
	case *ast.TypeSpec:
		return spec.Name
	case *ast.ValueSpec:
		for _, id := range spec.Names {
			if id.Name == sym.Name {
				return id
			}
		}
	}
	return nil
}

// groupOf reports whether the symbol lives inside a grouped declaration
// (const/var/type block with parentheses) and returns that declaration.
func groupOf(sym *workspace.Symbol) (*ast.GenDecl, bool) {
	gen, ok := sym.Decl().(*ast.GenDecl)
	return gen, ok && gen.Lparen.IsValid()
}

// recvNameOfType unwraps a receiver's types.Type down to its base type
// name — the semantic sibling of workspace.RecvTypeName, which does the
// same on the AST.
func recvNameOfType(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

// sortedKeys is the deterministic way to walk any map (invariant 6).
func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	return slices.Sorted(maps.Keys(m))
}

// symbolDoc returns the doc comment actually attached to sym's own spec —
// any parenthesized group, regardless of member count, since Go's parser
// attaches a per-spec comment to that spec, never to the enclosing
// GenDecl — or its declaration when sym isn't grouped at all. Matches
// EditSymbol's dispatch rule, not extractDecl's/DeleteSymbol's: those
// collapse a solo-member group to "ungrouped" for span purposes (correct
// there — removing the only member removes the whole group either way),
// but that collapse would look at the wrong CommentGroup here.
func symbolDoc(sym *workspace.Symbol) *ast.CommentGroup {
	if _, grouped := groupOf(sym); grouped {
		return workspace.DocOf(sym.Spec())
	}
	return workspace.DocOf(sym.Decl())
}

// leadingDocWord locates "prefix"+"want" at the very start of doc's first
// line (right after the "// " comment marker) and returns the span of
// just the "want" text, ok=false when the comment doesn't open with
// exactly that shape. This is what makes a doc-comment rewrite safe to
// automate: it only ever matches Go's own doc-comment conventions (a
// symbol's doc opens with its bare name; a package's doc opens with
// "Package name"), never prose that merely happens to mention the same
// word — see AGENTS.md's note on why renames stop at that boundary.
func (tx *Tx) leadingDocWord(file address.RelativePath, doc *ast.CommentGroup, prefix, want string) (span, bool) {
	if doc == nil || len(doc.List) == 0 {
		return span{}, false
	}
	first := doc.List[0]
	body := strings.TrimPrefix(strings.TrimPrefix(first.Text, "//"), " ")
	bodyOffset := len(first.Text) - len(body)
	if !strings.HasPrefix(body, prefix+want) {
		return span{}, false
	}
	if rest := body[len(prefix+want):]; rest != "" {
		switch rest[0] {
		case ' ', '.', ',', ':', '\'':
		default:
			return span{}, false
		}
	}
	start := first.Pos() + token.Pos(bodyOffset+len(prefix))
	end := start + token.Pos(len(want))
	return tx.offsetSpan(file, start, end)
}

// soloGroup reports whether sym is ungrouped, or the only member of its
// parenthesized group — the boundary extraction and deletion treat as
// equivalent, since removing or extracting the only member leaves nothing
// behind either way. This is deliberately a different boundary than
// symbolDoc's: a solo-member group still keeps its own per-spec doc
// comment on the spec, never the enclosing GenDecl, so doc lookup must
// not collapse the two cases the way span-based extraction correctly does.
func soloGroup(gen *ast.GenDecl, grouped bool) bool {
	return !grouped || len(gen.Specs) == 1
}

// constPositionDependent reports whether sym's value is derived from its
// position in a grouped const declaration (iota, or inheriting the
// previous spec's expression) — the boundary Move and Delete both treat
// the whole group as the atomic unit, since acting on one member alone
// would corrupt the position-dependent value of every member after it.
func constPositionDependent(gen *ast.GenDecl, grouped bool, sym *workspace.Symbol) bool {
	if !grouped || gen.Tok != token.CONST {
		return false
	}
	spec, ok := sym.Spec().(*ast.ValueSpec)
	return ok && (len(spec.Values) == 0 || groupUsesIota(gen))
}

// groupPositionDependent reports whether any member of a const group is
// position-dependent — the whole-group counterpart to
// constPositionDependent (which answers the question for one member),
// used to decide whether an existing group is safe to merge new members
// into: a group with any position-dependent member never is.
func groupPositionDependent(gen *ast.GenDecl) bool {
	if gen.Tok != token.CONST {
		return false
	}
	for _, spec := range gen.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Values) == 0 {
			return true
		}
	}
	return groupUsesIota(gen)
}
