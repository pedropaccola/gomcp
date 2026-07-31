package tools

import (
	"context"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

type MoveFileInput struct {
	PkgPath     string  `json:"pkg_path"`
	FileName    string  `json:"file_name"`
	NewPkgPath  *string `json:"new_pkg_path,omitempty"`
	NewFileName *string `json:"new_file_name,omitempty"`
}

type MovePackageInput struct {
	PkgPath    string `json:"pkg_path"`
	NewPkgPath string `json:"new_pkg_path"`
}

type MoveSymbolInput struct {
	PkgPath      string   `json:"pkg_path"`
	SymbolKey    string   `json:"symbol_key,omitempty"`
	SymbolKeys   []string `json:"symbol_keys,omitempty"`
	NewPkgPath   *string  `json:"new_pkg_path,omitempty"`
	NewFileName  *string  `json:"new_file_name,omitempty"`
	NewSymbolKey *string  `json:"new_symbol_key,omitempty"`
}

func moveSymbol(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[MoveSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MoveSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		_, out, err := runEdit(ctx, st, cfg, func(tx *store.Tx) error {
			pkg, err := writeWorkspacePkg(tx.View, in.PkgPath)
			if err != nil {
				return err
			}
			var newPkg workspace.PackageID
			if newPkgPath := optStr(in.NewPkgPath); newPkgPath != "" {
				newPkg, err = writeWorkspacePkg(tx.View, newPkgPath)
				if err != nil {
					return err
				}
			}
			newFile := optStr(in.NewFileName)
			if len(in.SymbolKeys) > 0 {
				if in.SymbolKey != "" {
					return fmt.Errorf("give symbol_key or symbol_keys, not both")
				}
				if optStr(in.NewSymbolKey) != "" {
					return fmt.Errorf("symbol_keys can't be combined with new_symbol_key: rename one symbol at a time with symbol_key, then move the group")
				}
				return tx.MoveSymbolGroup(pkg.Base(), in.SymbolKeys, newPkg.Base(), newFile)
			}
			if in.SymbolKey == "" {
				return fmt.Errorf("give symbol_key or symbol_keys")
			}
			return tx.MoveSymbol(pkg.Base(), in.SymbolKey, newPkg.Base(), newFile, optStr(in.NewSymbolKey))
		})
		if err != nil {
			return nil, out, err
		}
		out.Files = pruneVacatedPackages(ctx, st, out.Files)
		return nil, out, nil
	}
}

func moveFile(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[MoveFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MoveFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		_, out, err := runEdit(ctx, st, cfg, func(tx *store.Tx) error {
			pkg, err := writeWorkspacePkg(tx.View, in.PkgPath)
			if err != nil {
				return err
			}
			var newPkg workspace.PackageID
			if newPkgPath := optStr(in.NewPkgPath); newPkgPath != "" {
				newPkg, err = writeWorkspacePkg(tx.View, newPkgPath)
				if err != nil {
					return err
				}
			}
			return tx.MoveFile(pkg.Base(), in.FileName, newPkg.Base(), optStr(in.NewFileName))
		})
		if err != nil {
			return nil, out, err
		}
		out.Files = pruneVacatedPackages(ctx, st, out.Files)
		return nil, out, nil
	}
}

func movePackage(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[MovePackageInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MovePackageInput) (*mcp.CallToolResult, WriteOutput, error) {
		_, out, err := runEdit(ctx, st, cfg, func(tx *store.Tx) error {
			pkg, err := writeWorkspacePkg(tx.View, in.PkgPath)
			if err != nil {
				return err
			}
			newPkg, err := writeWorkspacePkg(tx.View, in.NewPkgPath)
			if err != nil {
				return err
			}
			return tx.MovePackage(pkg.Base(), newPkg.Base())
		})
		if err != nil {
			return nil, out, err
		}
		out.Files = pruneVacatedPackages(ctx, st, out.Files)
		return nil, out, nil
	}
}

// pruneVacatedPackages drops any bucket in files whose package address no
// longer resolves to a unit once the transaction has committed — a move
// whose old address is now fully empty, not merely modified, so listing
// it beside the destination would read as "still lives here" when the
// package is actually gone.
func pruneVacatedPackages(ctx context.Context, st *store.Store, files map[string][]string) map[string][]string {
	if len(files) == 0 {
		return files
	}
	st.Read(ctx, func(v *store.View) error {
		units := v.UnitKeys()
		for addr := range files {
			if !slices.Contains(units, workspace.PackagePath(addr)) {
				delete(files, addr)
			}
		}
		return nil
	})
	return files
}
