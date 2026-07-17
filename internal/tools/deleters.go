package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
)

func deleteSymbol(eng *engine.Engine) mcp.ToolHandlerFor[DeleteSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Deletes) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("deletes must not be empty")
		}
		n := len(in.Deletes)
		pkgs := make([]address.PkgPath, n)
		for i, entry := range in.Deletes {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, batchErr("deletes", i, n, err)
			}
			pkgs[i] = pkg
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i, entry := range in.Deletes {
				if err := tx.DeleteSymbol(pkgs[i], entry.SymbolKey); err != nil {
					return batchErr("deletes", i, n, err)
				}
			}
			return nil
		})
	}
}

func deleteFile(eng *engine.Engine) mcp.ToolHandlerFor[DeleteFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Deletes) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("deletes must not be empty")
		}
		n := len(in.Deletes)
		pkgs := make([]address.PkgPath, n)
		files := make([]string, n)
		for i, entry := range in.Deletes {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, batchErr("deletes", i, n, err)
			}
			file, err := fileArg(eng.ModulePath(), pkg, entry.FileName)
			if err != nil {
				return nil, WriteOutput{}, batchErr("deletes", i, n, err)
			}
			pkgs[i] = pkg
			files[i] = file
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i := range in.Deletes {
				if err := tx.DeleteFile(pkgs[i], files[i]); err != nil {
					return batchErr("deletes", i, n, err)
				}
			}
			return nil
		})
	}
}

func deletePackage(eng *engine.Engine) mcp.ToolHandlerFor[DeletePackageInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeletePackageInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Deletes) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("deletes must not be empty")
		}
		n := len(in.Deletes)
		pkgs := make([]address.PkgPath, n)
		for i, entry := range in.Deletes {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, batchErr("deletes", i, n, err)
			}
			pkgs[i] = pkg
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i := range in.Deletes {
				if err := tx.DeletePackage(pkgs[i]); err != nil {
					return batchErr("deletes", i, n, err)
				}
			}
			return nil
		})
	}
}
