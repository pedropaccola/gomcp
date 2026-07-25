package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
	"github.com/pedropaccola/gomcp/internal/gate"
)

type CreateFileEntry struct {
	PkgPath  string  `json:"pkg_path"`
	FileName string  `json:"file_name"`
	Doc      *string `json:"doc,omitempty"`
}

// CreateFileInput creates one or more files in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails.
type CreateFileInput struct {
	Creates []CreateFileEntry `json:"creates"`
}

type CreatePackageEntry struct {
	PkgPath string  `json:"pkg_path"`
	Name    *string `json:"name,omitempty"`
}

// CreatePackageInput creates one or more packages in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails.
type CreatePackageInput struct {
	Creates []CreatePackageEntry `json:"creates"`
}

type CreateSymbolEntry struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
	Source   string `json:"source"`
}

// CreateSymbolInput creates several symbols in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails.
type CreateSymbolInput struct {
	Creates []CreateSymbolEntry `json:"creates"`
}

func createPackage(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[CreatePackageInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreatePackageInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("creates must not be empty")
		}
		n := len(in.Creates)
		return runEdit(ctx, eng, cfg, func(tx *gate.Tx) error {
			for i, entry := range in.Creates {
				pkg, err := packageArg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("creates", i, n, err)
				}
				if err := tx.CreatePackage(pkg, optStr(entry.Name)); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}

func createFile(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[CreateFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("creates must not be empty")
		}
		n := len(in.Creates)
		return runEdit(ctx, eng, cfg, func(tx *gate.Tx) error {
			for i, entry := range in.Creates {
				pkg, err := packageArg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("creates", i, n, err)
				}
				file, err := fileArg(tx.Module(), pkg, entry.FileName)
				if err != nil {
					return batchErr("creates", i, n, err)
				}
				if err := tx.CreateFile(pkg, file, optStr(entry.Doc)); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}

func createSymbol(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[CreateSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("creates must not be empty")
		}
		n := len(in.Creates)
		return runEdit(ctx, eng, cfg, func(tx *gate.Tx) error {
			for i, entry := range in.Creates {
				pkg, err := packageArg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("creates", i, n, err)
				}
				file, err := fileArg(tx.Module(), pkg, entry.FileName)
				if err != nil {
					return batchErr("creates", i, n, err)
				}
				if err := tx.CreateSymbol(pkg, file, entry.Source); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}
