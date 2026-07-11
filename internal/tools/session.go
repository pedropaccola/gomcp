package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

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

func reload(eng *engine.Engine) mcp.ToolHandlerFor[ReloadInput, ReloadOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ReloadInput) (*mcp.CallToolResult, ReloadOutput, error) {
		var out ReloadOutput
		discarded, err := eng.Reload(ctx)
		if err != nil {
			return nil, out, err
		}
		out.FilesDiscarded = filesByPackage(eng.ModulePath(), discarded)
		err = eng.Read(func(v *engine.View) error {
			out.DiagBlock = diagBlock(v.AllDiagnostics())
			return nil
		})
		return nil, out, err
	}
}
