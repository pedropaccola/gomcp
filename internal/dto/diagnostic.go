package dto

import (
	"fmt"

	"github.com/pedropaccola/gomcp/internal/address"
)

// Diagnostic is a source-agnostic problem report: dto's own copy of the
// workspace's finding, safe to hold past the Read/Edit closure that
// produced it since it carries no pointer into the live model. Attribution
// is by declaration, not position: Package/Key name the enclosing
// declaration when one resolves, File is the coarser fallback for a
// diagnostic attributable to a file but no single declaration (import
// blocks, unparsed syntax), and both are empty for module/driver-level
// problems.
type Diagnostic struct {
	File    address.RelativePath // "" when not attributable to a workspace file
	Package address.PkgPath      // "" when not attributable to a package
	Key     string               // enclosing declaration's key; "" when not attributable to one
	Kind    DiagKind
	Msg     string
}

func (d Diagnostic) String() string {
	switch {
	case d.Key != "":
		return fmt.Sprintf("[%s] %s.%s: %s", d.Kind, d.Package, d.Key, d.Msg)
	case d.File != "":
		return fmt.Sprintf("[%s] %s: %s", d.Kind, d.File, d.Msg)
	default:
		return fmt.Sprintf("[%s] %s", d.Kind, d.Msg)
	}
}
