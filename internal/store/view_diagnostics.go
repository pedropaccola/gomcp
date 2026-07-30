package store

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/pedropaccola/gomcp/internal/workspace"
)

// AllDiagnostics aggregates every address's diagnostics, in path order.
func (v *View) AllDiagnostics() []Diagnostic {
	var out []Diagnostic
	for _, pkg := range v.ws.UnitKeys() {
		out = append(out, v.Diagnostics(pkg)...)
	}
	return out
}

// Diagnostics aggregates one package address's package- and file-scoped
// diagnostics across its Prod and XTest packages.
func (v *View) Diagnostics(pkg workspace.PackagePath) []Diagnostic {
	unit, ok := v.ws.Unit(pkg)
	if !ok {
		return nil
	}
	var out []workspace.Diagnostic
	for _, p := range unit.Members() {
		out = append(out, p.Diags...)
		for _, file := range p.Files() {
			out = append(out, file.Diags...)
		}
	}
	sortDiagnostics(out)
	return v.attributeDiagnostics(out, pkg)
}

// SymbolDiagnostics filters the owning file's diagnostics to those whose
// position falls inside the symbol's declaration span, doc comment
// included. It is a positional view, never the inventory: diagnostics that
// fall outside every declaration remain visible only at file scope and
// coarser.
func (v *View) SymbolDiagnostics(pkg workspace.PackagePath, key string) []Diagnostic {
	return newDiagnostics(v.ws.SymbolDiagnostics(pkg, key), pkg, key)
}

func (v *View) attributeDiagnostics(ds []workspace.Diagnostic, fallback workspace.PackagePath) []Diagnostic {
	if ds == nil {
		return nil
	}
	out := make([]Diagnostic, len(ds))
	for i, d := range ds {
		pkg, key := fallback, ""
		if d.File != "" {
			if p, k, ok := v.ws.AddressAtLine(d.File, d.Line); ok {
				pkg, key = p, k
			} else if _, owner, ok := v.ws.ResolveFileByPath(d.File); ok {
				pkg = owner.ID.Base()
			}
		}
		out[i] = newDiagnostic(d, pkg, key)
	}
	return out
}

// FileDiagnostics narrows Diagnostics to path's own file- and
// declaration-scoped problems — the file-granularity view between
// Diagnostics (whole package) and SymbolDiagnostics (one declaration).
func (v *View) FileDiagnostics(pkg workspace.PackagePath, path workspace.FilePath) []Diagnostic {
	all := v.Diagnostics(pkg)
	out := make([]Diagnostic, 0, len(all))
	for _, d := range all {
		if d.File == path {
			out = append(out, d)
		}
	}
	return out
}

// Diagnostic is a source-agnostic problem report: store's own copy,
// safe to hold past the Read/Edit closure that produced it. Attribution
// is by declaration, not position: Package/Key name the enclosing
// declaration when one resolves, File is the coarser fallback for a
// diagnostic attributable to a file but no single declaration (import
// blocks, unparsed syntax), and both are empty for module/driver-level
// problems. Kind stays workspace.DiagKind directly — unlike SymbolKind,
// nothing in internal/tools ever spells this type by name, so there's
// nothing to dissolve or duplicate.
type Diagnostic struct {
	File    workspace.FilePath
	Package workspace.PackagePath
	Key     string
	Kind    workspace.DiagKind
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

// newDiagnostic copies one workspace diagnostic into store's shape,
// attributing it to pkg/key when the caller has already resolved them.
func newDiagnostic(d workspace.Diagnostic, pkg workspace.PackagePath, key string) Diagnostic {
	return Diagnostic{File: d.File, Package: pkg, Key: key, Kind: d.Kind, Msg: d.Msg}
}

// newDiagnostics copies a slice of workspace diagnostics into store's
// shape, all sharing the same pkg/key attribution (e.g. every diagnostic
// inside one symbol's span), preserving nil for an empty slice.
func newDiagnostics(ds []workspace.Diagnostic, pkg workspace.PackagePath, key string) []Diagnostic {
	if ds == nil {
		return nil
	}
	out := make([]Diagnostic, len(ds))
	for i, d := range ds {
		out[i] = newDiagnostic(d, pkg, key)
	}
	return out
}

// sortDiagnostics orders problem reports by position, then message —
// determinism (invariant 6) for every diagnostics aggregation.
func sortDiagnostics(diags []workspace.Diagnostic) {
	slices.SortFunc(diags, func(a, b workspace.Diagnostic) int {
		if c := cmp.Compare(a.File, b.File); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Line, b.Line); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Col, b.Col); c != 0 {
			return c
		}
		return cmp.Compare(a.Msg, b.Msg)
	})
}
