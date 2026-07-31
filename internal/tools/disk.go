package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
)

type FlushInput struct{}

type FlushOutput struct {
	FilesWritten map[string][]string `json:"files_written,omitempty"`
	FilesRemoved map[string][]string `json:"files_removed,omitempty"`
}

type ReloadInput struct{}

// ReloadOutput reports what a reload threw away, grouped by package, plus
// the fresh workspace diagnostics — reload's scope is the whole workspace,
// so here the view and the inventory coincide.
type ReloadOutput struct {
	FilesDiscarded map[string][]string `json:"files_discarded,omitempty"`
	DiagnosticsTruncated
}

func flush(st *store.Store) mcp.ToolHandlerFor[FlushInput, FlushOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ FlushInput) (*mcp.CallToolResult, FlushOutput, error) {
		written, removed, err := st.Flush()
		return nil, FlushOutput{
			FilesWritten: filesByPackage(written),
			FilesRemoved: filesByPackage(removed),
		}, err
	}
}

func reload(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ReloadInput, ReloadOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ReloadInput) (*mcp.CallToolResult, ReloadOutput, error) {
		var out ReloadOutput
		discarded, err := st.Reload(ctx)
		if err != nil {
			return nil, out, err
		}
		out.FilesDiscarded = filesByPackage(discarded)
		err = st.Read(ctx, func(v *store.View) error {
			out.DiagnosticsTruncated = newDiagnosticsTruncated(v.AllDiagnostics(), cfg.diagLimit)
			return nil
		})
		return nil, out, err
	}
}
