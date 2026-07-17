package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
)

func editFile(eng *engine.Engine) mcp.ToolHandlerFor[EditFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Edits) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("edits must not be empty")
		}
		n := len(in.Edits)
		pkgs := make([]address.PkgPath, n)
		seen := make(map[string]bool, n)
		for i, entry := range in.Edits {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, batchErr("edits", i, n, err)
			}
			pkgs[i] = pkg
			addr := string(pkg) + "\x00" + entry.FileName
			if seen[addr] {
				return nil, WriteOutput{}, fmt.Errorf("edits[%d]: duplicate target %q in %q — a batch must address each file once", i, entry.FileName, pkg)
			}
			seen[addr] = true
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i, entry := range in.Edits {
				if err := tx.EditFile(pkgs[i], entry.FileName, optStr(entry.Doc)); err != nil {
					return batchErr("edits", i, n, err)
				}
			}
			return nil
		})
	}
}

func editSymbol(eng *engine.Engine) mcp.ToolHandlerFor[EditSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Edits) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("edits must not be empty")
		}
		n := len(in.Edits)
		pkgs := make([]address.PkgPath, n)
		seen := make(map[string]bool, n)
		for i, entry := range in.Edits {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, batchErr("edits", i, n, err)
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
					return batchErr("edits", i, n, err)
				}
			}
			return nil
		})
	}
}
