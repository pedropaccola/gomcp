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
		pkg, err := packageArg(eng, in.PkgPath)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreatePackage(pkg, optStr(in.Name))
		})
	}
}

func createFile(eng *engine.Engine) mcp.ToolHandlerFor[CreateFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		pkg, err := packageArg(eng, in.PkgPath)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		file, err := fileArg(eng.ModulePath(), pkg, in.FileName)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreateFile(pkg, file, optStr(in.Doc))
		})
	}
}

func createSymbol(eng *engine.Engine) mcp.ToolHandlerFor[CreateSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		pkg, err := packageArg(eng, in.PkgPath)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		file, err := fileArg(eng.ModulePath(), pkg, in.FileName)
		if err != nil {
			return nil, WriteOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreateSymbol(pkg, file, in.Source)
		})
	}
}

func createSymbolBatch(eng *engine.Engine) mcp.ToolHandlerFor[CreateSymbolBatchInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateSymbolBatchInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("creates must not be empty")
		}
		pkgs := make([]address.PkgPath, len(in.Creates))
		files := make([]string, len(in.Creates))
		for i, entry := range in.Creates {
			pkg, err := packageArg(eng, entry.PkgPath)
			if err != nil {
				return nil, WriteOutput{}, fmt.Errorf("creates[%d]: %w", i, err)
			}
			file, err := fileArg(eng.ModulePath(), pkg, entry.FileName)
			if err != nil {
				return nil, WriteOutput{}, fmt.Errorf("creates[%d]: %w", i, err)
			}
			pkgs[i] = pkg
			files[i] = file
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			for i, entry := range in.Creates {
				if err := tx.CreateSymbol(pkgs[i], files[i], entry.Source); err != nil {
					return fmt.Errorf("creates[%d]: %w", i, err)
				}
			}
			return nil
		})
	}
}
