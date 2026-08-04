package workspace

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Fragment is a validated piece of agent-supplied source: the symbol keys
// it declares, its placement classification, whether it references iota
// (only meaningful for a const-group fragment), and each declared key's
// own directive lines as parsed straight out of the fragment's source —
// the "new" side of a symbol-level directive diff, computed once here
// rather than by re-parsing src a second time.
type Fragment struct {
	Keys             []string
	Kind             SymbolKind
	Recv             string
	UsesIota         bool
	SymbolDirectives map[string][]string
}

// classifyFragment derives keys and placement class by reusing the same
// indexer that builds the real symbol tables. A single agent-supplied
// fragment never legitimately declares the same name twice, so taking
// the first (only) entry per key is always correct here — the
// multi-entry case IndexAST's shared map type accommodates is a
// RebuildIndex-only concern (multiple files), not a fragment one.
func classifyFragment(astFile *ast.File) Fragment {
	symbols := make(map[string][]*Symbol)
	inits := IndexAST("fragment.go", astFile, symbols)
	frag := Fragment{Keys: slices.Sorted(maps.Keys(symbols)), SymbolDirectives: make(map[string][]string, len(symbols))}
	for _, key := range frag.Keys {
		frag.Kind = symbols[key][0].Kind
		frag.Recv = symbols[key][0].Recv
		frag.SymbolDirectives[key] = symbols[key][0].Directives
	}
	for range inits {
		frag.Keys = append(frag.Keys, "init")
		frag.Kind = KindFunc
	}
	if len(astFile.Decls) == 1 {
		if gen, ok := astFile.Decls[0].(*ast.GenDecl); ok && gen.Tok == token.CONST {
			frag.UsesIota = GroupUsesIota(gen)
		}
	}
	return frag
}

// parseDeclFragment validates src as exactly one top-level declaration.
func parseDeclFragment(src string) (Fragment, error) {
	astFile, err := parser.ParseFile(token.NewFileSet(), "fragment.go", "package p\n\n"+src+"\n", parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return Fragment{}, fmt.Errorf("declaration does not parse: %w", err)
	}
	if len(astFile.Decls) != 1 {
		return Fragment{}, fmt.Errorf("expected exactly one top-level declaration, got %d", len(astFile.Decls))
	}
	if gen, ok := astFile.Decls[0].(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
		return Fragment{}, fmt.Errorf("import declarations are managed by the server")
	}
	return classifyFragment(astFile), nil
}

// parseSpecFragment validates src as one or more specs of a grouped
// declaration with the given keyword.
func parseSpecFragment(tok token.Token, src string) (Fragment, error) {
	wrapped := fmt.Sprintf("package p\n\n%s (\n%s\n)\n", tok, src)
	astFile, err := parser.ParseFile(token.NewFileSet(), "fragment.go", wrapped, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return Fragment{}, fmt.Errorf("spec does not parse inside a %s group: %w", tok, err)
	}
	return classifyFragment(astFile), nil
}

// constVarEntries parses src as a const or var declaration and returns each
// spec's own source text (doc comment included, the group keyword and
// parens stripped) joined together, plus the first spec's explicit type
// name if it has one. Used only by CreateSymbol's placement decisions:
// merging a new plain const/var into an existing group needs the specs
// alone, and clustering a typed iota group next to its type needs the
// type name — both need the parsed declaration itself, not just
// classifyFragment's summary.
func constVarEntries(src string) (specs string, typeName string, err error) {
	wrapped := "package p\n\n" + src + "\n"
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, "fragment.go", wrapped, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return "", "", err
	}
	gen, ok := astFile.Decls[0].(*ast.GenDecl)
	if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
		return "", "", fmt.Errorf("not a const or var declaration")
	}
	var parts []string
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		start := vs.Pos()
		if vs.Doc != nil {
			start = vs.Doc.Pos()
		}
		parts = append(parts, wrapped[fset.Position(start).Offset:fset.Position(vs.End()).Offset])
		if typeName == "" && vs.Type != nil {
			if ident, ok := vs.Type.(*ast.Ident); ok {
				typeName = ident.Name
			}
		}
	}
	return strings.Join(parts, "\n"), typeName, nil
}

// RenderDocComment formats plain text as a leading Go doc comment, one
// line comment per line, blank lines kept bare (no trailing space) per
// gofmt's own convention. Empty input renders to nothing.
func RenderDocComment(doc string) []byte {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return nil
	}
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		if line == "" {
			b.WriteString("//\n")
		} else {
			b.WriteString("// " + line + "\n")
		}
	}
	return []byte(b.String())
}

// SymbolDoc returns the doc comment actually attached to sym's own spec —
// any parenthesized group, regardless of member count, since Go's parser
// attaches a per-spec comment to that spec, never to the enclosing
// GenDecl — or its declaration when sym isn't grouped at all. Matches
// EditSymbol's dispatch rule, not extractDecl's/DeleteSymbol's: those
// collapse a solo-member group to "ungrouped" for span purposes (correct
// there — removing the only member removes the whole group either way),
// but that collapse would look at the wrong CommentGroup here.
func SymbolDoc(sym *Symbol) *ast.CommentGroup {
	if _, grouped := sym.GroupOf(); grouped {
		return DocOf(sym.Spec())
	}
	return DocOf(sym.Decl())
}

// ImportsPath reports whether the file already imports path, so the import
// self-repair never splices a duplicate.
func ImportsPath(astFile *ast.File, path string) bool {
	for _, imp := range astFile.Imports {
		if imp.Path.Value == strconv.Quote(path) {
			return true
		}
	}
	return false
}
