package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
)

type DiagnosticsInput struct{}

type DiagnosticsOutput struct {
	Diagnostics []DiagnosticEntry `json:"diagnostics"`
}

func diagnostics(eng *store.Store) mcp.ToolHandlerFor[DiagnosticsInput, DiagnosticsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ DiagnosticsInput) (*mcp.CallToolResult, DiagnosticsOutput, error) {
		var out DiagnosticsOutput
		err := eng.Read(ctx, func(v *store.View) error {
			diags := v.AllDiagnostics()
			out.Diagnostics = make([]DiagnosticEntry, len(diags))
			for i, diag := range diags {
				out.Diagnostics[i] = newDiagnosticEntry(diag)
			}
			return nil
		})
		return nil, out, err
	}
}
