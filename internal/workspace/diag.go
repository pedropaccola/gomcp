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
	File address.RelativePath // "" when not attributable to a workspace file
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
