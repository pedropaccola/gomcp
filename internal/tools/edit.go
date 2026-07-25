package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
	"github.com/pedropaccola/gomcp/internal/gate"
)

// WriteOutput is the shared echo of every write tool (creators, editors,
// refactorings alike): the files changed grouped by package, the
// diagnostics this edit introduced and resolved (each nil when there's
// nothing to report, not an empty block), how many pre-existing
// diagnostics it left untouched, and whether those two diagnostics blocks
// can be trusted at all.
type WriteOutput struct {
	Files                     map[string][]string   `json:"files"`
	IntroducedDiagnostics     *DiagnosticsTruncated `json:"introduced_diagnostics,omitempty"`
	ResolvedDiagnostics       *DiagnosticsTruncated `json:"resolved_diagnostics,omitempty"`
	UnrelatedDiagnosticsCount *int                  `json:"unrelated_diagnostics_count,omitempty"`
	DiagnosticsUnavailable    *bool                 `json:"diagnostics_unavailable,omitempty"`
}

// runEdit is the composite every write handler flows through: one
// transaction, echoed as files changed plus the diagnostics delta.
func runEdit(ctx context.Context, eng *engine.Engine, cfg *toolConfig, fn func(*gate.Tx) error) (*mcp.CallToolResult, WriteOutput, error) {
	var out WriteOutput
	report, err := eng.Edit(ctx, fn)
	if err != nil {
		return nil, out, err
	}
	out.Files = filesByPackage(eng.ModulePath(), report.Changed)
	if introduced := newDiagnosticsTruncated(report.Delta, cfg.diagLimit); len(introduced.Diagnostics) > 0 || introduced.Truncated != nil {
		out.IntroducedDiagnostics = &introduced
	}
	if resolved := newDiagnosticsTruncated(report.Resolved, cfg.diagLimit); len(resolved.Diagnostics) > 0 || resolved.Truncated != nil {
		out.ResolvedDiagnostics = &resolved
	}
	out.UnrelatedDiagnosticsCount = new(report.Unrelated)
	if report.Stale {
		out.DiagnosticsUnavailable = new(report.Stale)
		out.IntroducedDiagnostics = &DiagnosticsTruncated{Diagnostics: []DiagnosticEntry{{Message: "diagnostics unavailable: " + report.Note}}}
	}
	return nil, out, nil
}

// packageArg validates and canonicalizes a package address for the
// mutation handlers — the write-side gate: dependencies are refused, the
// workspace is the only mutable world. Takes a *gate.View (never eng
// *engine.Engine directly) so it's safe to call from inside a Read/Edit
// closure too — View never acquires the gate lock itself.
func packageArg(v *gate.View, addr string) (address.PkgPath, error) {
	canon, err := canonPkg(v.Module(), addr)
	if err != nil {
		return "", err
	}
	if clean, ok := address.CleanPath(addr); ok {
		if _, ok := v.ExternalPackage(address.PkgPath(clean)); ok {
			return "", fmt.Errorf("dependency %q is read-only", addr)
		}
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

// batchErr labels a batch entry's error with its index — unless the batch
// has exactly one entry, in which case the error reads exactly as it would
// from a lone, non-batch call: the array shape shouldn't tax the
// overwhelmingly common single-entry case with index noise.
func batchErr(field string, i, n int, err error) error {
	if n == 1 {
		return err
	}
	return fmt.Errorf("%s[%d]: %w", field, i, err)
}
