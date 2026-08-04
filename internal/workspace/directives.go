package workspace

import (
	"go/ast"
	"regexp"
	"strings"
)

// directivePattern matches the shape go/ast's own directive recognizer
// requires: a lowercase-alnum tag, a colon, then another alnum, right
// after the "//" with no space — //go:build, //go:generate, //go:embed,
// and any other //tool:directive alike.
var directivePattern = regexp.MustCompile(`^[a-z0-9]+:[a-z0-9]`)

// directiveLine reports whether text — one raw "//..." comment, as
// ast.Comment.Text — is directive-shaped, and returns its content with
// the leading "//" stripped.
func directiveLine(text string) (string, bool) {
	rest, ok := strings.CutPrefix(text, "//")
	if !ok || !directivePattern.MatchString(rest) {
		return "", false
	}
	return rest, true
}

// commentGroupDirectives extracts directive-shaped lines from a single
// comment group, in order — the symbol-level detection, where a
// directive and its doc prose are allowed to share one contiguous group
// with no isolation rule (that's a file-leading/go:build requirement
// only). Nil cg (no comment) yields no directives.
func commentGroupDirectives(cg *ast.CommentGroup) []string {
	if cg == nil {
		return nil
	}
	var out []string
	for _, c := range cg.List {
		if line, ok := directiveLine(c.Text); ok {
			out = append(out, line)
		}
	}
	return out
}

// fileDirectives extracts directive-shaped lines from every comment group
// positioned before astFile's package clause, in source order — the
// file-leading detection. A file-scoped directive can live in its own
// isolated group (required for //go:build, which needs a blank line
// before whatever follows) or, harmlessly, inside the file's own doc
// comment group if the two happen to be contiguous.
func fileDirectives(astFile *ast.File) []string {
	var out []string
	for _, cg := range astFile.Comments {
		if cg.Pos() >= astFile.Package {
			break // ast.File.Comments is in source order: nothing after this leads
		}
		out = append(out, commentGroupDirectives(cg)...)
	}
	return out
}

// RenderDirectives formats directives as consecutive leading comment
// lines — no space after "//", the grammar a real directive requires —
// followed by exactly one blank line separating them from whatever
// follows (a doc comment or the package clause directly). Empty input
// renders to nothing.
func RenderDirectives(directives []string) []byte {
	if len(directives) == 0 {
		return nil
	}
	var b strings.Builder
	for _, d := range directives {
		b.WriteString("//" + d + "\n")
	}
	b.WriteString("\n")
	return []byte(b.String())
}

// DiffDirectives reports which directive lines were added and which were
// removed going from old to new — the shared comparison behind every
// directive-change report, file-level and symbol-level alike. Order
// follows new/old respectively; a line present in both is neither added
// nor removed, regardless of its position.
func DiffDirectives(old, new []string) (added, removed []string) {
	oldSet := make(map[string]bool, len(old))
	for _, d := range old {
		oldSet[d] = true
	}
	newSet := make(map[string]bool, len(new))
	for _, d := range new {
		newSet[d] = true
	}
	for _, d := range new {
		if !oldSet[d] {
			added = append(added, d)
		}
	}
	for _, d := range old {
		if !newSet[d] {
			removed = append(removed, d)
		}
	}
	return added, removed
}
