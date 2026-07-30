package workspace

import (
	"go/ast"
	"go/token"
)

// SymbolKind classifies a top-level declaration.
type SymbolKind int

const (
	KindFunc SymbolKind = iota
	KindMethod
	KindType
	KindVar
	KindConst
)

var symbolKindNames = [...]string{"func", "method", "type", "var", "const"}

func (k SymbolKind) String() string {
	if k >= 0 && int(k) < len(symbolKindNames) {
		return symbolKindNames[k]
	}
	return "unknown"
}

// Symbol is one addressable top-level declaration: the unit of every read
// and every mutation. decl (and spec, for grouped members) point into the
// owning file's Ast, which by invariant parses exactly its Src — so a
// Symbol locates byte spans but never survives a rebuild (see
// RebuildIndex). Both are unexported: Decl/Spec reach outside this package
// only through the Decl()/Spec() accessors, never by field access.
type Symbol struct {
	Name string
	File FilePath
	Kind SymbolKind
	Recv string   // receiver type name; set only for KindMethod
	decl ast.Decl // the top-level declaration: the splice point for mutations
	spec ast.Spec // the symbol's own spec when decl is a grouped GenDecl
}

// Doc is derived from the AST on demand so it cannot go stale after mutations.
// The doc on the individual spec wins over the grouped declaration's doc.
func (s *Symbol) Doc() string {
	if s.spec != nil {
		if text := DocOf(s.spec).Text(); text != "" {
			return text
		}
	}
	return DocOf(s.decl).Text()
}

// Key is the symbol-index key: "Recv.Name" for methods, "Name" otherwise.
func (s *Symbol) Key() string {
	if s.Kind == KindMethod && s.Recv != "" {
		return s.Recv + "." + s.Name
	}
	return s.Name
}

// Decl returns the symbol's top-level declaration — the splice point for
// mutations.
func (s *Symbol) Decl() ast.Decl { return s.decl }

// Spec returns the symbol's own spec when Decl is a grouped GenDecl, nil
// otherwise.
func (s *Symbol) Spec() ast.Spec { return s.spec }

// DefiningIdent returns the identifier that declares s.
// Exported so store's rename verb shares this instead of keeping its
// own copy — a pure method over Symbol's own already-exported Decl()/
// Spec(), no workspace-internal state involved.
func (s *Symbol) DefiningIdent() *ast.Ident {
	if fn, ok := s.Decl().(*ast.FuncDecl); ok {
		return fn.Name
	}
	switch spec := s.Spec().(type) {
	case *ast.TypeSpec:
		return spec.Name
	case *ast.ValueSpec:
		for _, id := range spec.Names {
			if id.Name == s.Name {
				return id
			}
		}
	}
	return nil
}

// DocOf returns the doc comment attached to a declaration or spec, nil when
// there is none. The single authority on where a node's documentation lives —
// extraction and mutation splicing must agree on it.
func DocOf(node ast.Node) *ast.CommentGroup {
	switch n := node.(type) {
	case *ast.FuncDecl:
		return n.Doc
	case *ast.GenDecl:
		return n.Doc
	case *ast.TypeSpec:
		return n.Doc
	case *ast.ValueSpec:
		return n.Doc
	}
	return nil
}

// IndexAST fills symbols with astFile's top-level declarations, attributed
// to path, and returns its init functions — the one indexer, behind
// RebuildIndex and usable on bare ASTs that have no model File.
func IndexAST(path FilePath, astFile *ast.File, symbols map[string]*Symbol) (inits []*ast.FuncDecl) {
	for _, decl := range astFile.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			if node.Recv == nil && node.Name.Name == "init" {
				inits = append(inits, node)
				continue
			}
			sym := &Symbol{
				Name: node.Name.Name,
				File: path,
				Kind: KindFunc,
				decl: node,
			}
			if node.Recv != nil {
				sym.Kind = KindMethod
				sym.Recv = RecvTypeName(node.Recv)
			}
			symbols[sym.Key()] = sym
		case *ast.GenDecl:
			switch node.Tok {
			case token.TYPE:
				for _, spec := range node.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						symbols[typeSpec.Name.Name] = &Symbol{
							Name: typeSpec.Name.Name,
							File: path,
							Kind: KindType,
							decl: node,
							spec: typeSpec,
						}
					}
				}
			case token.VAR, token.CONST:
				symbolKind := KindVar
				if node.Tok == token.CONST {
					symbolKind = KindConst
				}
				for _, spec := range node.Specs {
					if valueSpec, ok := spec.(*ast.ValueSpec); ok {
						for _, id := range valueSpec.Names {
							if id.Name == "_" {
								continue
							}
							symbols[id.Name] = &Symbol{
								Name: id.Name,
								File: path,
								Kind: symbolKind,
								decl: node,
								spec: valueSpec,
							}
						}
					}
				}
			}
		}
	}
	return inits
}

// RecvTypeName unwraps a method receiver down to its base type name,
// handling pointer, parenthesized, and generic (T[P]) receivers.
func RecvTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	typ := recv.List[0].Type
	for {
		switch t := typ.(type) {
		case *ast.StarExpr:
			typ = t.X
		case *ast.ParenExpr:
			typ = t.X
		case *ast.IndexExpr:
			typ = t.X
		case *ast.IndexListExpr:
			typ = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}
