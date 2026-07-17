package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
)

func createPackage(eng *engine.Engine) mcp.ToolHandlerFor[CreatePackageInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreatePackageInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("creates must not be empty")
		}
		n := len(in.Creates)
		pkgs := make([]address.PkgPath, n)
		for i, entry := range in.Creates {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, batchErr("creates", i, n, err)
			}
			pkgs[i] = pkg
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i, entry := range in.Creates {
				if err := tx.CreatePackage(pkgs[i], optStr(entry.Name)); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}

func createFile(eng *engine.Engine) mcp.ToolHandlerFor[CreateFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("creates must not be empty")
		}
		n := len(in.Creates)
		pkgs := make([]address.PkgPath, n)
		files := make([]string, n)
		for i, entry := range in.Creates {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, batchErr("creates", i, n, err)
			}
			file, err := fileArg(eng.ModulePath(), pkg, entry.FileName)
			if err != nil {
				return nil, WriteOutput{}, batchErr("creates", i, n, err)
			}
			pkgs[i] = pkg
			files[i] = file
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i, entry := range in.Creates {
				if err := tx.CreateFile(pkgs[i], files[i], optStr(entry.Doc)); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}

func createSymbol(eng *engine.Engine) mcp.ToolHandlerFor[CreateSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("creates must not be empty")
		}
		n := len(in.Creates)
		pkgs := make([]address.PkgPath, n)
		files := make([]string, n)
		for i, entry := range in.Creates {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, batchErr("creates", i, n, err)
			}
			file, err := fileArg(eng.ModulePath(), pkg, entry.FileName)
			if err != nil {
				return nil, WriteOutput{}, batchErr("creates", i, n, err)
			}
			pkgs[i] = pkg
			files[i] = file
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i, entry := range in.Creates {
				if err := tx.CreateSymbol(pkgs[i], files[i], entry.Source); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}
