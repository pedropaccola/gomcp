package gate

import (
	"cmp"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// leadingDocWord locates "prefix"+"want" at the very start of doc's first
// line (right after the "// " comment marker) and returns the span of
// just the "want" text, ok=false when the comment doesn't open with
// exactly that shape. This is what makes a doc-comment rewrite safe to
// automate: it only ever matches Go's own doc-comment conventions (a
// symbol's doc opens with its bare name; a package's doc opens with
// "Package name"), never prose that merely happens to mention the same
// word.
func (tx *Tx) leadingDocWord(file address.FilePath, doc *ast.CommentGroup, prefix, want string) (span, bool) {
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

// offsetSpan converts a position range into byte offsets in the file's Src —
// the primitive under both source extraction and mutation splicing. Valid
// because Ast is by invariant a parse of exactly Src. Positions resolve in
// the owner's FileSet, so dependency files extract like workspace ones.
func (v *View) offsetSpan(path address.FilePath, from, to token.Pos) (span, bool) {
	file, owner, ok := v.resolveFileByPath(path)
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

// span is a byte-offset range [start, end) into a file's canonical Src.
type span struct{ start, end int }

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
	if _, grouped := sym.GroupOf(); grouped {
		return workspace.DocOf(sym.Spec())
	}
	return workspace.DocOf(sym.Decl())
}
