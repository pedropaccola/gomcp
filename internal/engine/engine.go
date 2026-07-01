package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/packages"
)

type RelativePath string

// CleanPath is the constructor for untrusted path strings (agent input):
// together with [Engine.relativePath] it is one of the only two doors through
// which a string becomes a RelativePath. It normalizes equivalent spellings
// and rejects addresses that cannot live inside the workspace — absolute
// paths and paths escaping the root.
func CleanPath(s string) (RelativePath, bool) {
	p := RelativePath(filepath.Clean(s))
	if filepath.IsAbs(string(p)) || p.escapesRoot() {
		return "", false
	}
	return p, true
}

// Clean re-normalizes the path so equivalent spellings of the same address
// ("./x", "x/", "a//b") resolve identically. Resolvers apply it on entry.
func (p RelativePath) Clean() RelativePath {
	return RelativePath(filepath.Clean(string(p)))
}

// Dir is the path of the containing directory ("." for root-level paths).
func (p RelativePath) Dir() RelativePath {
	return RelativePath(filepath.Dir(string(p)))
}

// Join appends a name to the path.
func (p RelativePath) Join(name string) RelativePath {
	return RelativePath(filepath.Join(string(p), name))
}

func (p RelativePath) String() string {
	return string(p)
}

// escapesRoot reports whether the path points outside the workspace root.
func (p RelativePath) escapesRoot() bool {
	return p == ".." || strings.HasPrefix(string(p), ".."+string(filepath.Separator))
}

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

type DiagKind int

const (
	DiagUnknown DiagKind = iota
	DiagList
	DiagParse
	DiagType
)

var diagKindNames = [...]string{"unknown", "list", "parse", "type"}

func (k DiagKind) String() string {
	if k >= 0 && int(k) < len(diagKindNames) {
		return diagKindNames[k]
	}
	return "unknown"
}

// Diagnostic is a source-agnostic problem report. Today it is filled from
// [packages.Error] during bootstrap; later sources (type re-checks after
// mutations) must funnel into the same shape.
type Diagnostic struct {
	File RelativePath // "" when not attributable to a workspace file
	Line int
	Col  int
	Kind DiagKind
	Msg  string
}

func (d Diagnostic) String() string {
	if d.File == "" {
		return fmt.Sprintf("[%s] %s", d.Kind, d.Msg)
	}
	return fmt.Sprintf("[%s] %s:%d:%d: %s", d.Kind, d.File, d.Line, d.Col, d.Msg)
}

type Symbol struct {
	Name string
	File RelativePath
	Kind SymbolKind
	Recv string   // receiver type name; set only for KindMethod
	Decl ast.Decl // the top-level declaration: the splice point for mutations
	Spec ast.Spec // the symbol's own spec when Decl is a grouped GenDecl
}

// Key is the [Package].Symbols map key: "Recv.Name" for methods, Name otherwise.
func (s *Symbol) Key() string {
	if s.Kind == KindMethod && s.Recv != "" {
		return s.Recv + "." + s.Name
	}
	return s.Name
}

// Doc is derived from the AST on demand so it cannot go stale after mutations.
// The doc on the individual spec wins over the grouped declaration's doc.
func (s *Symbol) Doc() string {
	if s.Spec != nil {
		if text := docOf(s.Spec).Text(); text != "" {
			return text
		}
	}
	return docOf(s.Decl).Text()
}

// File invariant: Src is the canonical bytes and Ast is always a parse of
// exactly Src. Mutations must print the new AST and re-parse before storing.
type File struct {
	Path    RelativePath
	Src     []byte
	Ast     *ast.File
	Inits   []*ast.FuncDecl
	Diags   []Diagnostic
	IsDirty bool
}

// index fills the file's Inits and adds its top-level symbols to symbols.
func (f *File) index(symbols map[string]*Symbol) {
	f.Inits = nil
	for _, decl := range f.Ast.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			if node.Recv == nil && node.Name.Name == "init" {
				f.Inits = append(f.Inits, node)
				continue
			}
			sym := &Symbol{
				Name: node.Name.Name,
				File: f.Path,
				Kind: KindFunc,
				Decl: node,
			}
			if node.Recv != nil {
				sym.Kind = KindMethod
				sym.Recv = recvTypeName(node.Recv)
			}
			symbols[sym.Key()] = sym
		case *ast.GenDecl:
			switch node.Tok {
			case token.TYPE:
				for _, spec := range node.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						symbols[typeSpec.Name.Name] = &Symbol{
							Name: typeSpec.Name.Name,
							File: f.Path,
							Kind: KindType,
							Decl: node,
							Spec: typeSpec,
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
								File: f.Path,
								Kind: symbolKind,
								Decl: node,
								Spec: valueSpec,
							}
						}
					}
				}
			}
		}
	}
}

type Package struct {
	Name    string
	Path    RelativePath
	PkgPath string // import path; the pattern for single-package overlay reloads
	Files   map[RelativePath]*File
	Symbols map[string]*Symbol // derived index; see RebuildIndex
	Diags   []Diagnostic       // package-scoped: no usable file position

	// Type information for the whole package, nil when type-checking could
	// not run at all. Populated per bootstrap; a broken package still gets
	// partial info alongside its DiagType diagnostics.
	Types     *types.Package
	TypesInfo *types.Info
}

// RebuildIndex re-derives Symbols and every file's Inits from the current
// ASTs. Call after any file's Ast is replaced; nothing is patched in place.
func (p *Package) RebuildIndex() {
	p.Symbols = make(map[string]*Symbol)
	for _, file := range p.Files {
		file.index(p.Symbols)
	}
}

// Unit holds the packages of one workspace directory: the production package
// (with in-package test files folded in) and the external _test package.
type Unit struct {
	Prod  *Package
	XTest *Package
}

type Engine struct {
	mu       sync.RWMutex
	RootDir  string
	FileSet  *token.FileSet
	Packages map[RelativePath]*Unit
	Diags    []Diagnostic // workspace-scoped: module/driver-level problems
	logf     func(string, ...any)

	// removed maps tombstoned paths (deleted or renamed away in-memory) to
	// the overlay mask that hides their on-disk content from rechecks; Flush
	// unlinks them. go/packages overlays cannot remove files, only replace
	// their content, hence the mask.
	removed map[RelativePath][]byte
}

// NewEngine creates an engine rooted at rootDir. logf enables go/packages
// loader debug output; nil means silent.
func NewEngine(rootDir string, logf func(string, ...any)) *Engine {
	return &Engine{
		RootDir:  rootDir,
		FileSet:  token.NewFileSet(),
		Packages: make(map[RelativePath]*Unit),
		logf:     logf,
		removed:  make(map[RelativePath][]byte),
	}
}

// absPath maps a workspace-relative path back to the filesystem.
func (e *Engine) absPath(p RelativePath) string {
	return filepath.Join(e.RootDir, string(p))
}

// Bootstrap loads the workspace from scratch and atomically swaps it in,
// discarding any in-memory edits and tombstones. Broken code is a valid
// state: per-package load errors become Diagnostics, never a bootstrap
// failure. Only a driver-level failure returns an error, leaving any
// previous state untouched.
func (e *Engine) Bootstrap(ctx context.Context) error {
	fset, units, err := e.load(ctx, nil)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.FileSet = fset
	e.Packages = units
	e.Diags = nil
	e.removed = make(map[RelativePath][]byte)
	e.mu.Unlock()
	return nil
}

// load runs the full pipeline — go/packages load, variant selection, package
// building — against disk plus an optional overlay of in-memory contents.
// It is the shared machinery of Bootstrap and the post-mutation recheck.
func (e *Engine) load(ctx context.Context, overlay map[string][]byte) (*token.FileSet, map[RelativePath]*Unit, error) {
	fset := token.NewFileSet()
	loadStart := time.Now()
	srcPkgs, err := packages.Load(&packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Context: ctx,
		Logf:    e.logf,
		Dir:     e.RootDir,
		Fset:    fset,
		Tests:   true,
		Overlay: overlay,
	}, "./...")
	if err != nil {
		return nil, nil, fmt.Errorf("workspace loading failure: %w", err)
	}
	if e.logf != nil {
		e.logf("load: go/packages took %v for %d package variants (overlay: %d files)",
			time.Since(loadStart), len(srcPkgs), len(overlay))
	}
	buildStart := time.Now()

	// Pass 1: select which load variants to keep, before building anything.
	type candidates struct {
		prod, xtest *packages.Package
	}
	selected := make(map[string]*candidates) // keyed by srcPkg.Dir
	for _, srcPkg := range srcPkgs {
		// Skip the synthesized test binary ("foo.test"): its only syntax is a
		// generated _testmain.go living outside the workspace.
		if strings.HasSuffix(srcPkg.ID, ".test") {
			continue
		}
		cand := selected[srcPkg.Dir]
		if cand == nil {
			cand = &candidates{}
			selected[srcPkg.Dir] = cand
		}
		switch {
		case strings.HasSuffix(srcPkg.Name, "_test"):
			cand.xtest = srcPkg
		// The test-expanded variant "foo [foo.test]" shares PkgPath with the
		// plain "foo" but is a superset of its files: prefer the widest.
		case cand.prod == nil || len(srcPkg.Syntax) > len(cand.prod.Syntax):
			cand.prod = srcPkg
		}
	}

	// Pass 2: build only the winners.
	units := make(map[RelativePath]*Unit)
	for _, cand := range selected {
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("workspace load aborted by context cancellation: %w", ctx.Err())
		}
		unit := &Unit{}
		if cand.prod != nil {
			if unit.Prod, err = e.buildPackage(cand.prod, fset, overlay); err != nil {
				return nil, nil, err
			}
		}
		if cand.xtest != nil {
			if unit.XTest, err = e.buildPackage(cand.xtest, fset, overlay); err != nil {
				return nil, nil, err
			}
		}
		switch {
		case unit.Prod != nil:
			units[unit.Prod.Path] = unit
		case unit.XTest != nil:
			units[unit.XTest.Path] = unit
		}
	}
	if e.logf != nil {
		e.logf("load: select+build took %v for %d units", time.Since(buildStart), len(units))
	}
	return fset, units, nil
}

func (e *Engine) buildPackage(srcPkg *packages.Package, fset *token.FileSet, overlay map[string][]byte) (*Package, error) {
	relPath, err := e.relativePath(srcPkg.Dir)
	if err != nil {
		return nil, fmt.Errorf("package mapping failure for %s: %w", srcPkg.Dir, err)
	}
	pkg := &Package{
		Name:      srcPkg.Name,
		Path:      relPath,
		PkgPath:   srcPkg.PkgPath,
		Files:     make(map[RelativePath]*File),
		Types:     srcPkg.Types,
		TypesInfo: srcPkg.TypesInfo,
	}

	for _, astFile := range srcPkg.Syntax {
		absFilePath := fset.File(astFile.FileStart).Name()
		relFilePath, err := e.relativePath(absFilePath)
		if err != nil || relFilePath.escapesRoot() {
			// Generated files (e.g. cgo output) live outside the workspace;
			// record and move on rather than tracking untouchable paths.
			pkg.Diags = append(pkg.Diags, Diagnostic{
				Kind: DiagList,
				Msg:  fmt.Sprintf("skipped file outside workspace: %s", absFilePath),
			})
			continue
		}
		src, ok := overlay[absFilePath]
		if !ok {
			var err error
			if src, err = os.ReadFile(absFilePath); err != nil {
				return nil, fmt.Errorf("failed to read source of %s: %w", relFilePath, err)
			}
		}
		pkg.Files[relFilePath] = &File{
			Path: relFilePath,
			Src:  src,
			Ast:  astFile,
		}
	}
	pkg.RebuildIndex()
	e.ingestErrors(pkg, srcPkg.Errors)
	return pkg, nil
}

// ingestErrors converts load errors into Diagnostics, attaching them to the
// file they point at when it is tracked, and to the package otherwise.
func (e *Engine) ingestErrors(pkg *Package, errs []packages.Error) {
	for _, pkgErr := range errs {
		// go list relays compiler output prefixed with "# pkg" and positions
		// pointing into overlay temp copies; the same problems arrive again
		// as parse/type errors with workspace positions, so the relay only
		// adds noise.
		if pkgErr.Kind == packages.ListError && strings.HasPrefix(pkgErr.Msg, "# ") {
			continue
		}
		diag := Diagnostic{Kind: toDiagKind(pkgErr.Kind), Msg: pkgErr.Msg}
		if absFile, line, col, ok := splitPos(pkgErr.Pos); ok {
			if relFile, err := e.relativePath(absFile); err == nil && !relFile.escapesRoot() {
				diag.File, diag.Line, diag.Col = relFile, line, col
			}
		}
		if file, ok := pkg.Files[diag.File]; ok && diag.File != "" {
			file.Diags = append(file.Diags, diag)
		} else {
			pkg.Diags = append(pkg.Diags, diag)
		}
	}
}

// Returns a path relative to [Engine]'s RootDir
func (e *Engine) relativePath(fullPath string) (RelativePath, error) {
	relPath, err := filepath.Rel(e.RootDir, fullPath)
	if err != nil {
		return "", err
	}
	return RelativePath(relPath), nil
}

func toDiagKind(kind packages.ErrorKind) DiagKind {
	switch kind {
	case packages.ListError:
		return DiagList
	case packages.ParseError:
		return DiagParse
	case packages.TypeError:
		return DiagType
	default:
		return DiagUnknown
	}
}

// splitPos parses the "file:line:col" / "file:line" / "file" position string
// of a [packages.Error]. ok is false when there is no position at all.
func splitPos(pos string) (file string, line, col int, ok bool) {
	if pos == "" || pos == "-" {
		return "", 0, 0, false
	}
	file = pos
	if i := strings.LastIndexByte(file, ':'); i >= 0 {
		if n, err := strconv.Atoi(file[i+1:]); err == nil {
			line, file = n, file[:i]
			if j := strings.LastIndexByte(file, ':'); j >= 0 {
				if m, err := strconv.Atoi(file[j+1:]); err == nil {
					line, col, file = m, n, file[:j]
				}
			}
		}
	}
	return file, line, col, true
}

// docOf returns the doc comment attached to a declaration or spec, nil when
// there is none. The single authority on where a node's documentation lives —
// extraction and (future) mutation splicing must agree on it.
func docOf(node ast.Node) *ast.CommentGroup {
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

// recvTypeName unwraps a method receiver down to its base type name,
// handling pointer, parenthesized, and generic (T[P]) receivers.
func recvTypeName(recv *ast.FieldList) string {
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
