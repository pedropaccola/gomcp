package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

type ListFilesInput struct {
	PkgPath string `json:"pkg_path"`
}

type ListFilesOutput struct {
	Files []FileEntry `json:"files"`
}

// ListMethodsInput addresses one type. FileName resolves and confirms
// the type declaration itself (an assertion, same convention as
// describe/edit/delete_symbols) — it does not scope the returned
// methods, which can live in any file (a method never has to share its
// receiver type's own file) and are still gathered by receiver-name
// match across every one of the package's members, as before.
type ListMethodsInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
	FileName  string `json:"file_name"`
}

type ListMethodsOutput struct {
	Methods []string `json:"methods"`
}

type ListPackagesInput struct{}

type ListPackagesOutput struct {
	Packages []string `json:"packages"`
}

type ListSymbolsInput struct {
	PkgPath  string  `json:"pkg_path"`
	FileName *string `json:"file_name,omitempty"`
}

type ListSymbolsOutput struct {
	Symbols []SymbolEntry `json:"symbols"`
}

type SymbolEntry struct {
	SymbolKey string `json:"symbol_key"`
	FileName  string `json:"file_name"`
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
}

// FileEntry is one file's identifying metadata as list_files reports it.
// PackageKind is omitted for the common Prod case, present only for
// XTest. Ignored and Generated are independent, orthogonal signals —
// either can land on either shape — each omitted when false.
type FileEntry struct {
	Name        string `json:"name"`
	PackageKind string `json:"package_kind,omitempty"`
	Ignored     bool   `json:"ignored,omitempty"`
	Generated   bool   `json:"generated,omitempty"`
}

func listPackages(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ListPackagesInput, ListPackagesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListPackagesInput) (*mcp.CallToolResult, ListPackagesOutput, error) {
		var out ListPackagesOutput
		err := st.Read(ctx, func(v *store.View) error {
			dirs := v.UnitKeys()
			out.Packages = make([]string, len(dirs))
			for i, dir := range dirs {
				out.Packages[i] = dir.String()
			}
			return nil
		})
		return nil, out, err
	}
}

func listFiles(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ListFilesInput, ListFilesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListFilesInput) (*mcp.CallToolResult, ListFilesOutput, error) {
		var out ListFilesOutput
		err := readPackage(ctx, st, in.PkgPath, func(v *store.View, pkg workspace.PackagePath) error {
			files, _ := v.PackageFiles(pkg)
			out.Files = make([]FileEntry, 0, len(files))
			for _, f := range files {
				entry := FileEntry{Name: f.Base()}
				entry.PackageKind, _ = v.FileKind(f)
				entry.Ignored, _ = v.FileIgnored(f)
				entry.Generated, _ = v.FileGenerated(f)
				out.Files = append(out.Files, entry)
			}
			return nil
		})
		return nil, out, err
	}
}

func listSymbols(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ListSymbolsInput, ListSymbolsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListSymbolsInput) (*mcp.CallToolResult, ListSymbolsOutput, error) {
		var out ListSymbolsOutput
		err := readPackage(ctx, st, in.PkgPath, func(v *store.View, pkg workspace.PackagePath) error {
			var targetFile workspace.FilePath
			if fileName := optStr(in.FileName); fileName != "" {
				fp, _, err := v.ResolveFile(pkg, fileName)
				if err != nil {
					return err
				}
				targetFile = fp
			}
			syms, _ := v.PackageSymbols(pkg)
			out.Symbols = make([]SymbolEntry, 0, len(syms))
			for _, sym := range syms {
				if targetFile != "" && sym.File != targetFile {
					continue
				}
				out.Symbols = append(out.Symbols, SymbolEntry{
					SymbolKey: sym.Key,
					FileName:  sym.File.Base(),
					Kind:      sym.Kind,
					Summary:   summarize(v, pkg, sym),
				})
			}
			return nil
		})
		return nil, out, err
	}
}

func listMethods(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ListMethodsInput, ListMethodsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListMethodsInput) (*mcp.CallToolResult, ListMethodsOutput, error) {
		var out ListMethodsOutput
		err := readPackage(ctx, st, in.PkgPath, func(v *store.View, pkg workspace.PackagePath) error {
			if _, ok := v.SymbolIn(pkg, in.SymbolKey, in.FileName); !ok {
				return fmt.Errorf("%s: call list_symbols for valid keys", workspace.NoSymbolError(in.SymbolKey, in.PkgPath))
			}
			out.Methods = methodSignatures(v, pkg, in.SymbolKey)
			return nil
		})
		return nil, out, err
	}
}

// summarize renders a symbol's one-line summary: the signature for funcs and
// methods, the trimmed first declaration line — doc comment skipped — for
// everything else.
func summarize(v *store.View, pkg workspace.PackagePath, sym store.Symbol) string {
	if sig, ok := v.Signature(pkg, sym.Key); ok {
		return sig
	}
	if src, ok := v.SpecSource(pkg, sym.Key); ok {
		for _, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) == 0 || strings.HasPrefix(trimmed, "//") {
				continue
			}
			return strings.TrimRight(trimmed, " \t{")
		}
	}
	return sym.Kind + " " + sym.Key
}
