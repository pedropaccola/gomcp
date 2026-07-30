package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
)

type DeleteFileEntry struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
}

// DeleteFileInput deletes one or more files in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails. Deletion is idempotent, so a duplicate target
// across entries is harmless, not refused.
type DeleteFileInput struct {
	Deletes []DeleteFileEntry `json:"deletes"`
}

type DeletePackageEntry struct {
	PkgPath string `json:"pkg_path"`
}

// DeletePackageInput deletes one or more packages in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails. Deletion is idempotent, so a duplicate target
// across entries is harmless, not refused.
type DeletePackageInput struct {
	Deletes []DeletePackageEntry `json:"deletes"`
}

type DeleteSymbolEntry struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

// DeleteSymbolInput deletes one or more symbols in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails. Deletion is idempotent, so a duplicate target
// across entries is harmless, not refused.
type DeleteSymbolInput struct {
	Deletes []DeleteSymbolEntry `json:"deletes"`
}

func deleteSymbol(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DeleteSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Deletes) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("deletes must not be empty")
		}
		n := len(in.Deletes)
		return runEdit(ctx, eng, cfg, func(tx *store.Tx) error {
			for i, entry := range in.Deletes {
				pkg, err := writeWorkspacePkg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("deletes", i, n, err)
				}
				if err := tx.DeleteSymbol(pkg.Base(), entry.SymbolKey); err != nil {
					return batchErr("deletes", i, n, err)
				}
			}
			return nil
		})
	}
}

func deleteFile(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DeleteFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Deletes) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("deletes must not be empty")
		}
		n := len(in.Deletes)
		return runEdit(ctx, eng, cfg, func(tx *store.Tx) error {
			for i, entry := range in.Deletes {
				pkg, err := writeWorkspacePkg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("deletes", i, n, err)
				}
				if err := tx.DeleteFile(pkg.Base(), entry.FileName); err != nil {
					return batchErr("deletes", i, n, err)
				}
			}
			return nil
		})
	}
}

func deletePackage(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DeletePackageInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeletePackageInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Deletes) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("deletes must not be empty")
		}
		n := len(in.Deletes)
		return runEdit(ctx, eng, cfg, func(tx *store.Tx) error {
			for i, entry := range in.Deletes {
				pkg, err := writeWorkspacePkg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("deletes", i, n, err)
				}
				if err := tx.DeletePackage(pkg.Base()); err != nil {
					return batchErr("deletes", i, n, err)
				}
			}
			return nil
		})
	}
}
