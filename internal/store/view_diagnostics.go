package store

import (
	"cmp"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// AllDiagnostics aggregates every address's diagnostics, in path order.
func (v *View) AllDiagnostics() []dto.Diagnostic {
	var out []dto.Diagnostic
	for _, pkg := range v.ws.UnitKeys() {
		out = append(out, v.Diagnostics(pkg)...)
	}
	return out
}

// Diagnostics aggregates one package address's package- and file-scoped
// diagnostics across its Prod and XTest packages.
func (v *View) Diagnostics(pkg address.PkgPath) []dto.Diagnostic {
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
func (v *View) SymbolDiagnostics(pkg address.PkgPath, key string) []dto.Diagnostic {
	return newDiagnostics(v.ws.SymbolDiagnostics(pkg, key), pkg, key)
}

func (v *View) attributeDiagnostics(ds []workspace.Diagnostic, fallback address.PkgPath) []dto.Diagnostic {
	if ds == nil {
		return nil
	}
	out := make([]dto.Diagnostic, len(ds))
	for i, d := range ds {
		pkg, key := fallback, ""
		if d.File != "" {
			if p, k, ok := v.ws.AddressAtLine(d.File, d.Line); ok {
				pkg, key = p, k
			} else if _, owner, ok := v.ws.ResolveFileByPath(d.File); ok {
				pkg = owner.PkgPath
			}
		}
		out[i] = newDiagnostic(d, pkg, key)
	}
	return out
}

// newDiagnostic copies one workspace diagnostic into dto's shape,
// attributing it to pkg/key when the caller has already resolved them.
func newDiagnostic(d workspace.Diagnostic, pkg address.PkgPath, key string) dto.Diagnostic {
	return dto.Diagnostic{File: d.File, Package: pkg, Key: key, Kind: dto.DiagKind(d.Kind), Msg: d.Msg}
}

// newDiagnostics copies a slice of workspace diagnostics into dto's
// shape, all sharing the same pkg/key attribution (e.g. every diagnostic
// inside one symbol's span), preserving nil for an empty slice.
func newDiagnostics(ds []workspace.Diagnostic, pkg address.PkgPath, key string) []dto.Diagnostic {
	if ds == nil {
		return nil
	}
	out := make([]dto.Diagnostic, len(ds))
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
