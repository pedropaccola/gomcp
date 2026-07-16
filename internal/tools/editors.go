package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
)

func editSymbol(eng *engine.Engine) mcp.ToolHandlerFor[EditSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		pkg, err := packageArg(eng, in.PkgPath)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.EditSymbol(pkg, in.SymbolKey, in.Source)
		})
	}
}

func editFile(eng *engine.Engine) mcp.ToolHandlerFor[EditFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		pkg, err := packageArg(eng, in.PkgPath)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.EditFile(pkg, in.FileName, optStr(in.Doc))
		})
	}
}

func editSymbolBatch(eng *engine.Engine) mcp.ToolHandlerFor[EditSymbolBatchInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditSymbolBatchInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Edits) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("edits must not be empty")
		}
		pkgs := make([]address.PkgPath, len(in.Edits))
		seen := make(map[string]bool, len(in.Edits))
		for i, entry := range in.Edits {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, fmt.Errorf("edits[%d]: %w", i, err)
			}
			pkgs[i] = pkg
			addr := string(pkg) + "\x00" + entry.SymbolKey
			if seen[addr] {
				return nil, WriteOutput{}, fmt.Errorf("edits[%d]: duplicate target %q in %q — a batch must address each symbol once", i, entry.SymbolKey, pkg)
			}
			seen[addr] = true
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i, entry := range in.Edits {
				if err := tx.EditSymbol(pkgs[i], entry.SymbolKey, entry.Source); err != nil {
					return fmt.Errorf("edits[%d]: %w", i, err)
				}
			}
			return nil
		})
	}
}
