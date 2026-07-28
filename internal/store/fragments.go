package store

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// fragment is a validated piece of agent-supplied source: the symbol keys
// it declares, its placement classification, and whether it references
// iota (only meaningful for a const-group fragment).
type fragment struct {
	keys     []string
	kind     dto.SymbolKind
	recv     string
	usesIota bool
}

// classifyFragment derives keys and placement class by reusing the same
// indexer that builds the real symbol tables.
func classifyFragment(astFile *ast.File) fragment {
	symbols := make(map[string]*workspace.Symbol)
	inits := workspace.IndexAST("fragment.go", astFile, symbols)
	frag := fragment{keys: sortedKeys(symbols)}
	for _, key := range frag.keys {
		frag.kind = dto.SymbolKind(symbols[key].Kind)
		frag.recv = symbols[key].Recv
	}
	for range inits {
		frag.keys = append(frag.keys, "init")
		frag.kind = dto.KindFunc
	}
	if len(astFile.Decls) == 1 {
		if gen, ok := astFile.Decls[0].(*ast.GenDecl); ok && gen.Tok == token.CONST {
			frag.usesIota = workspace.GroupUsesIota(gen)
		}
	}
	return frag
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

// importsPath reports whether the file already imports path, so the import
// self-repair never splices a duplicate.
func importsPath(astFile *ast.File, path string) bool {
	for _, imp := range astFile.Imports {
		if imp.Path.Value == strconv.Quote(path) {
			return true
		}
	}
	return false
}

// parseDeclFragment validates src as exactly one top-level declaration.
func parseDeclFragment(src string) (fragment, error) {
	astFile, err := parser.ParseFile(token.NewFileSet(), "fragment.go", "package p\n\n"+src+"\n", parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fragment{}, fmt.Errorf("declaration does not parse: %w", err)
	}
	if len(astFile.Decls) != 1 {
		return fragment{}, fmt.Errorf("expected exactly one top-level declaration, got %d", len(astFile.Decls))
	}
	if gen, ok := astFile.Decls[0].(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
		return fragment{}, fmt.Errorf("import declarations are managed by the server")
	}
	return classifyFragment(astFile), nil
}

// parseSpecFragment validates src as one or more specs of a grouped
// declaration with the given keyword.
func parseSpecFragment(tok token.Token, src string) (fragment, error) {
	wrapped := fmt.Sprintf("package p\n\n%s (\n%s\n)\n", tok, src)
	astFile, err := parser.ParseFile(token.NewFileSet(), "fragment.go", wrapped, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fragment{}, fmt.Errorf("spec does not parse inside a %s group: %w", tok, err)
	}
	return classifyFragment(astFile), nil
}

// renderDocComment formats plain text as a leading Go doc comment, one
// line comment per line, blank lines kept bare (no trailing space) per
// gofmt's own convention. Empty input renders to nothing.
func renderDocComment(doc string) []byte {
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
