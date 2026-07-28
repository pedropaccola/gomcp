package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/store"
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
func runEdit(ctx context.Context, eng *store.Store, cfg *toolConfig, fn func(*store.Tx) error) (*mcp.CallToolResult, WriteOutput, error) {
	var out WriteOutput
	report, err := eng.Edit(ctx, fn)
	if err != nil {
		return nil, out, err
	}
	out.Files = filesByPackage(report.Changed)
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

// writeWorkspacePkg validates and canonicalizes a package address for the
// mutation handlers — the write-side check: dependencies are refused, the
// workspace is the only mutable world. Takes a *store.View (never eng
// *store.Store directly) so it's safe to call from inside a Read/Edit
// closure too — View never acquires the store lock itself.
func writeWorkspacePkg(v *store.View, addr string) (address.PkgPath, error) {
	canon, err := address.NewPkgPath(v.Module(), addr)
	if err != nil {
		return "", err
	}
	if _, ok := v.ExternalPackage(address.PkgPath(addr)); ok {
		return "", fmt.Errorf("dependency %q is read-only", addr)
	}
	return canon, nil
}

// filesByPackage groups already-canonical file addresses into the
// interface's address convention: canonical package address to bare file
// names. Input order is preserved within each package, so sorted paths
// stay sorted. Every FilePath is already module-qualified by
// construction (pkg+"/"+basename), so its directory portion is already
// the exact package address — no separate composition needed.
func filesByPackage(paths []address.FilePath) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for _, p := range paths {
		key := p.Dir().String()
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
