package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// WriteOutput is the shared echo of every write tool (creators, editors,
// refactorings alike): the files changed grouped by package, the
// diagnostics this edit introduced and resolved (each nil when there's
// nothing to report, not an empty block), how many pre-existing
// diagnostics it left untouched — always present, since zero is itself
// meaningful and must stay distinguishable from "not computed" — and
// whether those two diagnostics blocks can be trusted at all. Any
// directive lines an edit added or removed report separately from
// diagnostics entirely, since they're not a compiler-sourced problem to
// fix, just a heads-up about what changed.
type WriteOutput struct {
	Files                     map[string][]string   `json:"files"`
	IntroducedDiagnostics     *DiagnosticsTruncated `json:"introduced_diagnostics,omitempty"`
	ResolvedDiagnostics       *DiagnosticsTruncated `json:"resolved_diagnostics,omitempty"`
	UnrelatedDiagnosticsCount int                   `json:"unrelated_diagnostics_count"`
	DiagnosticsUnavailable    *bool                 `json:"diagnostics_unavailable,omitempty"`
	DirectiveChanges          []DirectiveChange     `json:"directive_changes,omitempty"`
}

// runEdit is the composite every write handler flows through: one
// transaction, echoed as files changed plus the diagnostics delta.
func runEdit(ctx context.Context, st *store.Store, cfg *toolConfig, fn func(*store.Tx) error) (*mcp.CallToolResult, WriteOutput, error) {
	var out WriteOutput
	report, err := st.Edit(ctx, fn)
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
	out.UnrelatedDiagnosticsCount = report.Unrelated
	if report.Stale {
		out.DiagnosticsUnavailable = new(report.Stale)
		out.IntroducedDiagnostics = &DiagnosticsTruncated{Diagnostics: []DiagnosticEntry{{Message: "diagnostics unavailable: " + report.Note}}}
	}
	if len(report.DirectiveDeltas) > 0 {
		out.DirectiveChanges = make([]DirectiveChange, len(report.DirectiveDeltas))
		for i, d := range report.DirectiveDeltas {
			out.DirectiveChanges[i] = newDirectiveChange(d)
		}
	}
	return nil, out, nil
}

// writeWorkspacePkg validates and canonicalizes a package address for the
// mutation handlers — the write-side check: dependencies are refused, the
// workspace is the only mutable world. Takes a *store.View (never
// *store.Store directly) so it's safe to call from inside a Read/Edit
// closure too — View never acquires the store lock itself.
func writeWorkspacePkg(v *store.View, addr string) (workspace.PackagePath, error) {
	canon, err := workspace.NewPackagePath(v.Module(), addr)
	if err != nil {
		return "", err
	}
	if v.HasExternalPackage(workspace.PackagePath(addr)) {
		return "", fmt.Errorf("%q is a dependency: writes stay scoped to the workspace", addr)
	}
	return canon, nil
}

// filesByPackage groups already-canonical file addresses into the
// interface's address convention: canonical package address to bare file
// names. Input order is preserved within each package, so sorted paths
// stay sorted. Every FilePath is already module-qualified by
// construction (pkg+"/"+basename), so its directory portion is already
// the exact package address — no separate composition needed.
func filesByPackage(paths []workspace.FilePath) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for _, p := range paths {
		key := p.PackagePath().String()
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

// resolveBatchTargets resolves each batch entry's package address and
// rejects a batch that addresses the same (package, key, fileName) triple
// twice — the invariant editFile and editSymbol both need, each keyed by
// a different notion of "key" (a file name, a symbol key); fileName is
// always empty for editFile (a file name already fully addresses its own
// target) and the entry's own file discriminant for editSymbol, where two
// same-keyed entries scoped to different files are legitimately distinct
// targets, not a duplicate.
func resolveBatchTargets(v *store.View, n int, field, noun string, target func(i int) (pkgPath, key, fileName string)) ([]workspace.PackagePath, error) {
	pkgs := make([]workspace.PackagePath, n)
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		pkgPath, key, fileName := target(i)
		pkg, err := writeWorkspacePkg(v, pkgPath)
		if err != nil {
			return nil, batchErr(field, i, n, err)
		}
		pkgs[i] = pkg
		addr := pkgs[i].String() + "\x00" + key + "\x00" + fileName
		if seen[addr] {
			return nil, fmt.Errorf("%s[%d]: duplicate target %q in %q — a batch must address each %s once", field, i, key, pkg, noun)
		}
		seen[addr] = true
	}
	return pkgs, nil
}
