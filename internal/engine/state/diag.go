package state

import "fmt"

const (
	KindFunc SymbolKind = iota
	KindMethod
	KindType
	KindVar
	KindConst
)

var symbolKindNames = [...]string{"func", "method", "type", "var", "const"}

const (
	DiagUnknown DiagKind = iota
	DiagList
	DiagParse
	DiagType
)

var diagKindNames = [...]string{"unknown", "list", "parse", "type"}

// SymbolKind classifies a top-level declaration.
type SymbolKind int

func (k SymbolKind) String() string {
	if k >= 0 && int(k) < len(symbolKindNames) {
		return symbolKindNames[k]
	}
	return "unknown"
}

// DiagKind classifies a problem report by its source in the load pipeline.
type DiagKind int

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
