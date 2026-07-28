package workspace

import (
	"fmt"

	"github.com/pedropaccola/gomcp/internal/address"
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
	File address.FilePath // "" when not attributable to a workspace file
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
func (w *Workspace) SymbolDiagnostics(pkg address.PkgPath, key string) []Diagnostic {
	sym, owner, ok := w.ResolveSymbol(pkg, key)
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
