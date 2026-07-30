package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

type ListFilesInput struct {
	PkgPath string `json:"pkg_path"`
}

type ListFilesOutput struct {
	Files []string `json:"files"`
	DiagnosticsTruncated
}

type ListMethodsInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

type ListMethodsOutput struct {
	Methods []string `json:"methods"`
	DiagnosticsTruncated
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
	DiagnosticsTruncated
}

type SymbolEntry struct {
	SymbolKey string `json:"symbol_key"`
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
}

func listPackages(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ListPackagesInput, ListPackagesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListPackagesInput) (*mcp.CallToolResult, ListPackagesOutput, error) {
		var out ListPackagesOutput
		err := eng.Read(ctx, func(v *store.View) error {
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

func listFiles(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ListFilesInput, ListFilesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListFilesInput) (*mcp.CallToolResult, ListFilesOutput, error) {
		var out ListFilesOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *store.View, pkg workspace.PackageID) error {
			files, _ := v.PackageFiles(pkg)
			out.Files = make([]string, 0, len(files))
			for _, f := range files {
				out.Files = append(out.Files, f.Base())
			}
			out.DiagnosticsTruncated = newDiagnosticsTruncated(v.Diagnostics(pkg.Base()), cfg.diagLimit)
			return nil
		})
		return nil, out, err
	}
}

func listSymbols(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ListSymbolsInput, ListSymbolsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListSymbolsInput) (*mcp.CallToolResult, ListSymbolsOutput, error) {
		var out ListSymbolsOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *store.View, pkg workspace.PackageID) error {
			var targetFile workspace.FilePath
			if fileName := optStr(in.FileName); fileName != "" {
				fp, err := v.ResolveFile(pkg, fileName)
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
					Kind:      sym.Kind,
					Summary:   summarize(v, pkg.Base(), sym),
				})
			}
			diags := v.Diagnostics(pkg.Base())
			if targetFile != "" {
				diags = diagsForFile(diags, targetFile)
			}
			out.DiagnosticsTruncated = newDiagnosticsTruncated(diags, cfg.diagLimit)
			return nil
		})
		return nil, out, err
	}
}

func listMethods(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[ListMethodsInput, ListMethodsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListMethodsInput) (*mcp.CallToolResult, ListMethodsOutput, error) {
		var out ListMethodsOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *store.View, pkg workspace.PackageID) error {
			out.Methods = methodSignatures(v, pkg, in.SymbolKey)
			var diags []store.Diagnostic
			for _, m := range v.Methods(pkg, in.SymbolKey) {
				diags = append(diags, v.SymbolDiagnostics(pkg.Base(), m.Key)...)
			}
			out.DiagnosticsTruncated = newDiagnosticsTruncated(diags, cfg.diagLimit)
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
