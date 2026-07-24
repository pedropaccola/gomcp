package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
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

func flush(eng *engine.Engine) mcp.ToolHandlerFor[FlushInput, FlushOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ FlushInput) (*mcp.CallToolResult, FlushOutput, error) {
		written, removed, err := eng.Flush()
		module := eng.ModulePath()
		return nil, FlushOutput{
			FilesWritten: filesByPackage(module, written),
			FilesRemoved: filesByPackage(module, removed),
		}, err
	}
}

func reload(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[ReloadInput, ReloadOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ReloadInput) (*mcp.CallToolResult, ReloadOutput, error) {
		var out ReloadOutput
		discarded, err := eng.Reload(ctx)
		if err != nil {
			return nil, out, err
		}
		out.FilesDiscarded = filesByPackage(eng.ModulePath(), discarded)
		err = eng.Read(ctx, func(v *engine.View) error {
			out.DiagnosticsTruncated = newDiagnosticsTruncated(v.AllDiagnostics(), cfg.diagLimit)
			return nil
		})
		return nil, out, err
	}
}
