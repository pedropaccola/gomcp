package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
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
	PkgPath      string  `json:"pkg_path"`
	SymbolKey    string  `json:"symbol_key"`
	NewPkgPath   *string `json:"new_pkg_path,omitempty"`
	NewFileName  *string `json:"new_file_name,omitempty"`
	NewSymbolKey *string `json:"new_symbol_key,omitempty"`
}

func moveSymbol(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[MoveSymbolInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MoveSymbolInput) (*mcp.CallToolResult, WriteOutput, error) {
		return runEdit(ctx, eng, cfg, func(tx *engine.Tx) error {
			pkg, err := packageArg(tx.View, in.PkgPath)
			if err != nil {
				return err
			}
			var newPkg address.PkgPath
			destPkg := pkg
			if newPkgPath := optStr(in.NewPkgPath); newPkgPath != "" {
				newPkg, err = packageArg(tx.View, newPkgPath)
				if err != nil {
					return err
				}
				destPkg = newPkg
			}
			var newFile string
			if newFileName := optStr(in.NewFileName); newFileName != "" {
				newFile, err = fileArg(tx.Module(), destPkg, newFileName)
				if err != nil {
					return err
				}
			}
			return tx.MoveSymbol(pkg, in.SymbolKey, newPkg, newFile, optStr(in.NewSymbolKey))
		})
	}
}

func moveFile(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[MoveFileInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MoveFileInput) (*mcp.CallToolResult, WriteOutput, error) {
		return runEdit(ctx, eng, cfg, func(tx *engine.Tx) error {
			pkg, err := packageArg(tx.View, in.PkgPath)
			if err != nil {
				return err
			}
			file, err := fileArg(tx.Module(), pkg, in.FileName)
			if err != nil {
				return err
			}
			var newPkg address.PkgPath
			if newPkgPath := optStr(in.NewPkgPath); newPkgPath != "" {
				newPkg, err = packageArg(tx.View, newPkgPath)
				if err != nil {
					return err
				}
			}
			return tx.MoveFile(pkg, file, newPkg, optStr(in.NewFileName))
		})
	}
}

func movePackage(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[MovePackageInput, WriteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MovePackageInput) (*mcp.CallToolResult, WriteOutput, error) {
		return runEdit(ctx, eng, cfg, func(tx *engine.Tx) error {
			pkg, err := packageArg(tx.View, in.PkgPath)
			if err != nil {
				return err
			}
			newPkg, err := packageArg(tx.View, in.NewPkgPath)
			if err != nil {
				return err
			}
			return tx.MovePackage(pkg, newPkg)
		})
	}
}
