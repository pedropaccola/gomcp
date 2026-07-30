package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
)

type EditFileEntry struct {
	PkgPath  string  `json:"pkg_path"`
	FileName string  `json:"file_name"`
	Doc      *string `json:"doc,omitempty"`
}

// EditFileInput edits one or more files' package doc comments in one
// transaction, one recheck, one echo — resolved in order, the whole batch
// discarded on the first entry that fails. Every entry must address a
// different file; two entries targeting the same one are refused before
// the transaction opens.
type EditFileInput struct {
	Edits []EditFileEntry `json:"edits"`
}

type EditSymbolEntry struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
	Source    string `json:"source"`
}

// EditSymbolInput edits several symbols in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails. Every entry must address a different symbol;
// two entries targeting the same one, identical source or not, are
// refused before the transaction opens.
type EditSymbolInput struct {
	Edits []EditSymbolEntry `json:"edits"`
}

func editFile(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[EditFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Edits) == 0 {
			return nil, WriteOutput{}, errEmptyBatch("edits")
		}
		n := len(in.Edits)
		return runEdit(ctx, eng, cfg, func(tx *store.Tx) error {
			pkgs, err := resolveBatchTargets(tx.View, n, "edits", "file", func(i int) (string, string) {
				return in.Edits[i].PkgPath, in.Edits[i].FileName
			})
			if err != nil {
				return err
			}
			for i, entry := range in.Edits {
				if err := tx.EditFile(pkgs[i], entry.FileName, optStr(entry.Doc)); err != nil {
					return batchErr("edits", i, n, err)
				}
			}
			return nil
		})
	}
}

func editSymbol(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[EditSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Edits) == 0 {
			return nil, WriteOutput{}, errEmptyBatch("edits")
		}
		n := len(in.Edits)
		return runEdit(ctx, eng, cfg, func(tx *store.Tx) error {
			pkgs, err := resolveBatchTargets(tx.View, n, "edits", "symbol", func(i int) (string, string) {
				return in.Edits[i].PkgPath, in.Edits[i].SymbolKey
			})
			if err != nil {
				return err
			}
			for i, entry := range in.Edits {
				if err := tx.EditSymbol(pkgs[i], entry.SymbolKey, entry.Source); err != nil {
					return batchErr("edits", i, n, err)
				}
			}
			return nil
		})
	}
}
