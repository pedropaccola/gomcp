package gate

import (
	"cmp"
	"go/ast"
	"maps"
	"slices"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

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
