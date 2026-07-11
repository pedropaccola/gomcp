package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
)

// runEdit is the composite every write handler flows through: one
// transaction, echoed as files changed plus the diagnostics delta.
func runEdit(ctx context.Context, eng *engine.Engine, fn func(*engine.Tx) error) (*mcp.CallToolResult, WriteOutput, error) {
	var out WriteOutput
	report, err := eng.Edit(ctx, fn)
	if err != nil {
		return nil, out, err
	}
	out.Files = filesByPackage(eng.ModulePath(), report.Changed)
	out.IntroducedDiagnostics = diagBlockPtr(report.Delta)
	out.ResolvedDiagnostics = diagBlockPtr(report.Resolved)
	out.UnrelatedDiagnosticsCount = new(report.Unrelated)
	out.DiagnosticsUnavailable = new(report.Stale)
	if report.Stale {
		out.IntroducedDiagnostics = &DiagBlock{Diagnostics: []DiagnosticEntry{{Message: "diagnostics unavailable: " + report.Note}}}
	}
	return nil, out, nil
}

// packageArg validates and canonicalizes a package address for the
// mutation handlers — the write-side gate: dependencies are refused, the
// workspace is the only mutable world.
func packageArg(eng *engine.Engine, addr string) (address.PkgPath, error) {
	canon, err := canonPkg(eng.ModulePath(), addr)
	if err != nil {
		return "", err
	}
	if clean, ok := address.CleanPath(addr); ok && eng.IsExternal(address.PkgPath(clean)) {
		return "", fmt.Errorf("dependency %q is read-only", addr)
	}
	return canon, nil
}

// filesByPackage groups workspace-relative paths into the interface's
// address convention: canonical package address to bare file names. Input
// order is preserved within each package, so sorted paths stay sorted.
func filesByPackage(module address.PkgPath, paths []address.RelativePath) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for _, p := range paths {
		key := pkgAddr(module, p.Dir())
		out[key] = append(out[key], p.Base())
	}
	return out
}
