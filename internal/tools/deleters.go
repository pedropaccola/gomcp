package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

func deleteSymbol(eng *engine.Engine) mcp.ToolHandlerFor[DeleteSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		pkg, err := packageArg(eng, in.PkgPath)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeleteSymbol(pkg, in.SymbolKey)
		})
	}
}

func deleteFile(eng *engine.Engine) mcp.ToolHandlerFor[DeleteFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		pkg, err := packageArg(eng, in.PkgPath)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		file, err := fileArg(eng.ModulePath(), pkg, in.FileName)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeleteFile(pkg, file)
		})
	}
}

func deletePackage(eng *engine.Engine) mcp.ToolHandlerFor[DeletePackageInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeletePackageInput) (*mcp.CallToolResult, WriteOutput, error) {
		pkg, err := packageArg(eng, in.PkgPath)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeletePackage(pkg)
		})
	}
}
