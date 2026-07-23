package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
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

func editFile(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[EditFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Edits) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("edits must not be empty")
		}
		n := len(in.Edits)
		return runEdit(ctx, eng, cfg, func(tx *engine.Tx) error {
			pkgs := make([]address.PkgPath, n)
			seen := make(map[string]bool, n)
			for i, entry := range in.Edits {
				pkg, err := packageArg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("edits", i, n, err)
				}
				pkgs[i] = pkg
				addr := string(pkg) + "\x00" + entry.FileName
				if seen[addr] {
					return fmt.Errorf("edits[%d]: duplicate target %q in %q — a batch must address each file once", i, entry.FileName, pkg)
				}
				seen[addr] = true
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

func editSymbol(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[EditSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		if len(in.Edits) == 0 {
			return nil, WriteOutput{}, fmt.Errorf("edits must not be empty")
		}
		n := len(in.Edits)
		return runEdit(ctx, eng, cfg, func(tx *engine.Tx) error {
			pkgs := make([]address.PkgPath, n)
			seen := make(map[string]bool, n)
			for i, entry := range in.Edits {
				pkg, err := packageArg(tx.View, entry.PkgPath)
				if err != nil {
					return batchErr("edits", i, n, err)
				}
				pkgs[i] = pkg
				addr := string(pkg) + "\x00" + entry.SymbolKey
				if seen[addr] {
					return fmt.Errorf("edits[%d]: duplicate target %q in %q — a batch must address each symbol once", i, entry.SymbolKey, pkg)
				}
				seen[addr] = true
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
