package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
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

func createPackage(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[CreatePackageInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreatePackageInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, errEmptyBatch("creates")
		}
		n := len(in.Creates)
		return runEdit(ctx, st, cfg, func(tx *store.Tx) error {
			for i, entry := range in.Creates {
				pkg, err := writeWorkspacePkg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("creates", i, n, err)
				}
				if pkg.Kind() != workspace.KindProd {
					return batchErr("creates", i, n, fmt.Errorf("%q names an XTest half: create_packages always creates the Prod half — call create_files or create_symbols with this address instead, it creates the Prod package too if the whole package is new", entry.PkgPath))
				}
				if err := tx.CreatePackage(pkg.Base(), optStr(entry.Name)); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}

func createFile(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[CreateFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, errEmptyBatch("creates")
		}
		n := len(in.Creates)
		return runEdit(ctx, st, cfg, func(tx *store.Tx) error {
			for i, entry := range in.Creates {
				pkg, err := writeWorkspacePkg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("creates", i, n, err)
				}
				if err := tx.CreateFile(pkg, entry.FileName, optStr(entry.Doc)); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}

func createSymbol(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[CreateSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Creates) == 0 {
			return nil, WriteOutput{}, errEmptyBatch("creates")
		}
		n := len(in.Creates)
		return runEdit(ctx, st, cfg, func(tx *store.Tx) error {
			for i, entry := range in.Creates {
				pkg, err := writeWorkspacePkg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("creates", i, n, err)
				}
				if err := tx.CreateSymbol(pkg, entry.FileName, entry.Source); err != nil {
					return batchErr("creates", i, n, err)
				}
			}
			return nil
		})
	}
}
