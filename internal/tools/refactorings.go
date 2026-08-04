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

// MoveSymbolInput moves/renames one symbol (symbol_key) or relocates a
// caller-chosen batch of them together (symbol_keys) — the batch is not
// necessarily a single grouped declaration, so its members may span more
// than one file. file_name asserts the source file: for symbol_key, the
// declaration's own file, checked exactly; for symbol_keys, only the
// first key's own file — full per-member disambiguation across a
// multi-file batch isn't supported.
type MoveSymbolInput struct {
	PkgPath      string   `json:"pkg_path"`
	SymbolKey    string   `json:"symbol_key,omitempty"`
	SymbolKeys   []string `json:"symbol_keys,omitempty"`
	FileName     string   `json:"file_name"`
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
			var newPkg workspace.PackagePath
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
				// A symbol_keys batch can legitimately span multiple files (e.g. a
				// type and a method declared on it in a separate file) — unlike
				// grouped const/var declarations, this "group" is just a caller-
				// chosen batch, not one shared file. file_name can only assert
				// the first key's own file; it can't disambiguate every member
				// against a same-name collision the way the single-key path can.
				if err := validateMoveSource(tx.View, pkg, in.SymbolKeys[0], in.FileName); err != nil {
					return err
				}
				return tx.MoveSymbolGroup(pkg, in.SymbolKeys, newPkg, newFile)
			}
			if in.SymbolKey == "" {
				return fmt.Errorf("give symbol_key or symbol_keys")
			}
			if err := validateMoveSource(tx.View, pkg, in.SymbolKey, in.FileName); err != nil {
				return err
			}
			return tx.MoveSymbol(pkg, in.SymbolKey, newPkg, newFile, optStr(in.NewSymbolKey))
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
			var newPkg workspace.PackagePath
			if newPkgPath := optStr(in.NewPkgPath); newPkgPath != "" {
				newPkg, err = writeWorkspacePkg(tx.View, newPkgPath)
				if err != nil {
					return err
				}
			}
			return tx.MoveFile(pkg, in.FileName, newPkg, optStr(in.NewFileName))
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
			return tx.MovePackage(pkg, newPkg)
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

// validateMoveSource confirms key's own declaration lives exactly in
// fileName — an assertion, not a hint. Unlike describe/edit/delete_symbol,
// MoveSymbol's own rename/relocate machinery (ValidateNewName,
// renameSymbol, RelocateSymbols) re-resolves key by name internally at
// several points rather than carrying one resolved *Symbol through, so a
// genuine collision (a same-named declaration elsewhere, only possible
// when at least one is Ignored) can't be safely disambiguated deep
// inside that chain without a much larger rewrite. This validates the
// common, non-colliding case up front (fileName must match the one real
// declaration) and refuses outright — rather than silently acting on
// whichever one the internal primary-preference resolution would pick —
// whenever a real collision means fileName doesn't uniquely pin down key.
func validateMoveSource(v *store.View, pkg workspace.PackagePath, key, fileName string) error {
	scoped, ok := v.SymbolIn(pkg, key, fileName)
	if !ok {
		return fmt.Errorf("%s: no declaration named %q in file %q", workspace.NoSymbolError(key, pkg), key, fileName)
	}
	primary, ok := v.Symbol(pkg, key)
	if !ok || primary.File != scoped.File {
		return fmt.Errorf("%q is ambiguous: multiple declarations share this name across different files; move isn't supported while the collision exists — resolve it first (e.g. rename or delete the shadowing declaration)", key)
	}
	return nil
}
