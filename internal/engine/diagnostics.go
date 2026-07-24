package engine

import (
	"cmp"
	"slices"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// Diagnostics aggregates one package address's package- and file-scoped
// diagnostics across its Prod and XTest packages.
func (v *View) Diagnostics(pkg address.PkgPath) []Diagnostic {
	unit, ok := v.ws.Unit(pkg)
	if !ok {
		return nil
	}
	var out []workspace.Diagnostic
	for _, p := range []*workspace.Package{unit.Prod, unit.XTest} {
		if p == nil {
			continue
		}
		out = append(out, p.Diags...)
		for _, file := range p.Files() {
			out = append(out, file.Diags...)
		}
	}
	sortDiagnostics(out)
	return v.attributeDiagnostics(out)
}

// symbolDiagnostics filters the owning file's diagnostics to those whose
// position falls inside the symbol's declaration span, doc comment
// included, in the workspace's own model type. It is a positional view,
// never the inventory: diagnostics that fall outside every declaration
// remain visible only at file scope and coarser.
func (v *View) symbolDiagnostics(sym *workspace.Symbol) []workspace.Diagnostic {
	file, owner, ok := v.resolveFile(sym.File)
	if !ok {
		return nil
	}
	start := sym.Decl().Pos()
	if doc := workspace.DocOf(sym.Decl()); doc != nil {
		start = doc.Pos()
	}
	fset := v.fsetOf(owner)
	from := fset.Position(start).Line
	to := fset.Position(sym.Decl().End()).Line
	var out []workspace.Diagnostic
	for _, diag := range file.Diags {
		if diag.Line >= from && diag.Line <= to {
			out = append(out, diag)
		}
	}
	return out
}

// WorkspaceDiagnostics enumerates only the workspace-scoped diagnostics:
// module/driver-level problems not attributable to any package.
func (v *View) WorkspaceDiagnostics() []Diagnostic {
	return v.attributeDiagnostics(v.ws.WorkspaceDiags())
}

// AllDiagnostics aggregates workspace-scoped diagnostics followed by every
// address's, in path order.
func (v *View) AllDiagnostics() []Diagnostic {
	out := v.WorkspaceDiagnostics()
	for _, pkg := range v.ws.UnitKeys() {
		out = append(out, v.Diagnostics(pkg)...)
	}
	return out
}

// SymbolDiagnostics filters the owning file's diagnostics to those whose
// position falls inside the symbol's declaration span, doc comment
// included. It is a positional view, never the inventory: diagnostics that
// fall outside every declaration remain visible only at file scope and
// coarser.
func (v *View) SymbolDiagnostics(pkg address.PkgPath, key string) []Diagnostic {
	sym, _, ok := v.resolveSymbol(pkg, key)
	if !ok {
		return nil
	}
	return newDiagnostics(v.symbolDiagnostics(sym), pkg, sym.Key())
}

// attributeDiagnostics resolves each diagnostic's enclosing package and
// declaration before translating it into engine's own shape — the
// per-item counterpart of newDiagnostics for a set that may span multiple
// declarations or files, falling back to the owning package alone when a
// diagnostic lands outside every declaration, and to neither for
// module/driver-level problems.
func (v *View) attributeDiagnostics(ds []workspace.Diagnostic) []Diagnostic {
	if ds == nil {
		return nil
	}
	out := make([]Diagnostic, len(ds))
	for i, d := range ds {
		var pkg address.PkgPath
		var key string
		if d.File != "" {
			if sym, owner, ok := v.symbolFromLine(d.File, d.Line); ok {
				pkg, key = owner.PkgPath, sym.Key()
			} else if _, owner, ok := v.resolveFile(d.File); ok {
				pkg = owner.PkgPath
			}
		}
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

// diffDiagnostics compares a workspace's diagnostics inventory across a
// transaction: delta is what's newly present, resolved is what's newly
// absent, and unrelated counts diagnostics unchanged by either edge — the
// pre-existing breakage a transaction's echo deliberately stays silent
// about, visible only through the uncapped diagnostics tool.
func diffDiagnostics(before, after []Diagnostic) (delta, resolved []Diagnostic, unrelated int) {
	beforeSet := make(map[string]Diagnostic, len(before))
	for _, diag := range before {
		beforeSet[diag.String()] = diag
	}
	afterSet := make(map[string]bool, len(after))
	for _, diag := range after {
		key := diag.String()
		afterSet[key] = true
		if _, existed := beforeSet[key]; existed {
			unrelated++
		} else {
			delta = append(delta, diag)
		}
	}
	for _, key := range sortedKeys(beforeSet) {
		if !afterSet[key] {
			resolved = append(resolved, beforeSet[key])
		}
	}
	return delta, resolved, unrelated
}
