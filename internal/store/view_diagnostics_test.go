package store

import (
	"context"
	"go/token"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

func TestViewAllDiagnosticsAggregatesEveryUnit(t *testing.T) {
	ws := workspace.NewWorkspace()
	ws.Reset("test.mod", token.NewFileSet(), map[address.PkgPath]*workspace.Unit{})
	p1 := workspace.NewPackage("a", "test.mod/a", nil, nil, workspace.KindProd)
	p1.Diags = append(p1.Diags, workspace.Diagnostic{Kind: workspace.DiagParse, Msg: "a-broke"})
	p2 := workspace.NewPackage("b", "test.mod/b", nil, nil, workspace.KindProd)
	p2.Diags = append(p2.Diags, workspace.Diagnostic{Kind: workspace.DiagParse, Msg: "b-broke"})
	ws.InstallUnit("test.mod/a", workspace.NewUnit(p1, nil))
	ws.InstallUnit("test.mod/b", workspace.NewUnit(p2, nil))
	v := NewView(ws, context.Background())
	diags := v.AllDiagnostics()
	if len(diags) != 2 {
		t.Errorf("AllDiagnostics() = %+v, want both units' diagnostics", diags)
	}
}

func TestViewDiagnosticsPackageScoped(t *testing.T) {
	ws := workspace.NewWorkspace()
	ws.Reset("test.mod", token.NewFileSet(), map[address.PkgPath]*workspace.Unit{})
	wp := workspace.NewPackage("pkg", "test.mod/pkg", nil, nil, workspace.KindProd)
	wp.Diags = append(wp.Diags, workspace.Diagnostic{Kind: workspace.DiagParse, Msg: "boom"})
	ws.InstallUnit("test.mod/pkg", workspace.NewUnit(wp, nil))
	v := NewView(ws, context.Background())
	diags := v.Diagnostics("test.mod/pkg")
	if len(diags) != 1 || diags[0].Msg != "boom" {
		t.Errorf("Diagnostics(test.mod/pkg) = %+v, want one boom diagnostic", diags)
	}
}

func TestViewSymbolDiagnosticsScopedToSpan(t *testing.T) {
	v := viewFixture(t, "package pkg\n\nfunc Foo() {}\n\nfunc Bar() {}\n")
	if diags := v.SymbolDiagnostics("test.mod/pkg", "Foo"); diags != nil {
		t.Errorf("SymbolDiagnostics(Foo) = %+v, want none on a clean fixture", diags)
	}
}
