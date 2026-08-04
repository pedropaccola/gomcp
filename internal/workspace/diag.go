package workspace

import (
	"fmt"
	"go/token"
	"maps"
	"slices"
	"strings"
)

// DiagKind classifies a problem report by its source in the load pipeline.
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

// Diagnostic is a source-agnostic problem report, filled from
// [packages.Error] during loads; every later source (type re-checks after
// mutations) funnels into the same shape.
type Diagnostic struct {
	File FilePath // "" when not attributable to a workspace file
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

// SymbolDiagnostics filters key's owning file's diagnostics to those
// whose position falls inside its declaration span, doc comment
// included. A positional view, never the inventory: diagnostics outside
// every declaration remain visible only at file scope and coarser.
// fileName follows DeclSource's own assertion-vs-primary-preference
// convention.
func (w *Workspace) SymbolDiagnostics(pkg PackagePath, key, fileName string) []Diagnostic {
	var sym *Symbol
	var owner *Package
	var ok bool
	if fileName != "" {
		sym, owner, ok = w.ResolveSymbolIn(pkg, key, fileName)
	} else {
		sym, owner, ok = w.ResolveSymbol(pkg, key)
	}
	if !ok {
		return nil
	}
	file, ok := owner.File(sym.File)
	if !ok {
		return nil
	}
	start := sym.Decl().Pos()
	if doc := DocOf(sym.Decl()); doc != nil {
		start = doc.Pos()
	}
	fset := w.FsetOf(owner)
	from := fset.Position(start).Line
	to := fset.Position(sym.Decl().End()).Line
	var out []Diagnostic
	for _, diag := range file.Diags {
		if diag.Line >= from && diag.Line <= to {
			out = append(out, diag)
		}
	}
	return out
}

// AllDiagnostics aggregates every address's diagnostics, in address order.
func (w *Workspace) AllDiagnostics() []Diagnostic {
	var out []Diagnostic
	for _, addr := range w.MemberKeys() {
		for _, p := range w.MembersOf(addr) {
			out = append(out, p.Diagnostics()...)
		}
	}
	return out
}

// RepairMissingImports is the bounded self-repair pass behind Store.Edit:
// when a recheck reports "undefined: X" and X names exactly one
// in-memory package, the missing import is spliced in. goimports cannot
// discover packages that exist only in memory (it scans disk), and
// imports are not agent-addressable, so the model must cover its own
// blind spot. Best-effort by design: ambiguous names and failed splices
// leave the diagnostic standing, and a wrong repair (an ident that merely
// collides with a package name) surfaces as an ordinary diagnostic on the
// next echo while goimports drops the then-unused import on the file's
// next reload. Returns every path written, for the caller's own
// change-tracking, and whether anything was repaired at all.
func (w *Workspace) RepairMissingImports() (touched []FilePath, repaired bool) {
	// Unique importable package names known to the workspace.
	candidates := make(map[string]PackagePath) // package name -> import path
	ambiguous := make(map[string]bool)
	for _, addr := range w.MemberKeys() {
		pkg, _ := w.ProdPackage(addr)
		if pkg == nil || pkg.ID.Base() == "" || pkg.Name == "main" {
			continue
		}
		if _, dup := candidates[pkg.Name]; dup {
			ambiguous[pkg.Name] = true
			delete(candidates, pkg.Name)
			continue
		}
		candidates[pkg.Name] = pkg.ID.Base()
	}

	needed := make(map[FilePath]map[string]bool) // file -> import paths
	for _, diag := range w.AllDiagnostics() {
		if diag.Kind != DiagType || diag.File == "" {
			continue
		}
		name, found := strings.CutPrefix(diag.Msg, "undefined: ")
		if !found || !token.IsIdentifier(name) || ambiguous[name] {
			continue
		}
		path, ok := candidates[name]
		if !ok {
			continue
		}
		file, owner, ok := w.ResolveFileByPath(diag.File)
		if !ok || owner.ID.Base() == path || ImportsPath(file.Ast(), string(path)) {
			continue
		}
		if needed[diag.File] == nil {
			needed[diag.File] = make(map[string]bool)
		}
		needed[diag.File][string(path)] = true
	}

	for _, filePath := range slices.Sorted(maps.Keys(needed)) {
		file, owner, ok := w.ResolveFileByPath(filePath)
		if !ok {
			continue
		}
		var repl strings.Builder
		for _, path := range slices.Sorted(maps.Keys(needed[filePath])) {
			fmt.Fprintf(&repl, "\n\nimport %q", path)
		}
		insertAt := file.Ast().Name.End()
		sp, ok := w.NewSpliceAtPos(owner, filePath, insertAt, insertAt, []byte(repl.String()))
		if !ok {
			continue
		}
		candidate := ByteSplices{sp}.Apply(file.Src())
		addr := filePath.PackagePath()
		if err := w.SwapFile(addr, owner.ID.Kind(), file.Ignored, filePath, candidate); err != nil {
			continue // repair is best-effort; the diagnostic stays visible
		}
		touched = append(touched, filePath)
		repaired = true
	}
	return touched, repaired
}
