package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/engine"
	"github.com/pedropaccola/gomcp/internal/gate"
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

func listPackages(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[ListPackagesInput, ListPackagesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListPackagesInput) (*mcp.CallToolResult, ListPackagesOutput, error) {
		var out ListPackagesOutput
		err := eng.Read(ctx, func(v *gate.View) error {
			pkgs := v.Packages()
			out.Packages = make([]string, 0, len(pkgs))
			last := ""
			for _, pkg := range pkgs {
				if addr := pkg.Dir().String(); addr != last {
					out.Packages = append(out.Packages, addr)
					last = addr
				}
			}
			return nil
		})
		return nil, out, err
	}
}

func listFiles(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[ListFilesInput, ListFilesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListFilesInput) (*mcp.CallToolResult, ListFilesOutput, error) {
		var out ListFilesOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *gate.View, pkg dto.Package) error {
			files := pkg.Files()
			out.Files = make([]string, 0, len(files))
			for _, file := range files {
				out.Files = append(out.Files, file.Path().Name())
			}
			out.DiagnosticsTruncated = newDiagnosticsTruncated(v.Diagnostics(pkg.PkgPath()), cfg.diagLimit)
			return nil
		})
		return nil, out, err
	}
}

func listSymbols(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[ListSymbolsInput, ListSymbolsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListSymbolsInput) (*mcp.CallToolResult, ListSymbolsOutput, error) {
		var out ListSymbolsOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *gate.View, pkg dto.Package) error {
			var target *dto.File
			if fileName := optStr(in.FileName); fileName != "" {
				fp, err := address.NewFilePath(v.Module(), pkg.PkgPath(), fileName)
				if err != nil {
					return err
				}
				for _, f := range pkg.Files() {
					if f.Path() == fp {
						target = &f
						break
					}
				}
				if target == nil {
					return fmt.Errorf("no file %q in package %q", fp, in.PkgPath)
				}
			}
			syms := pkg.Symbols()
			out.Symbols = make([]SymbolEntry, 0, len(syms))
			for _, sym := range syms {
				if target != nil && sym.File() != target.Path() {
					continue
				}
				out.Symbols = append(out.Symbols, SymbolEntry{
					SymbolKey: sym.Key(),
					Kind:      sym.Kind().String(),
					Summary:   summarize(v, pkg, sym),
				})
			}
			diags := v.Diagnostics(pkg.PkgPath())
			if target != nil {
				diags = diagsForFile(diags, target.Path())
			}
			out.DiagnosticsTruncated = newDiagnosticsTruncated(diags, cfg.diagLimit)
			return nil
		})
		return nil, out, err
	}
}

func listMethods(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[ListMethodsInput, ListMethodsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListMethodsInput) (*mcp.CallToolResult, ListMethodsOutput, error) {
		var out ListMethodsOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *gate.View, pkg dto.Package) error {
			out.Methods = methodSignatures(v, pkg, in.SymbolKey)
			var diags []dto.Diagnostic
			for _, m := range v.Methods(pkg, in.SymbolKey) {
				diags = append(diags, v.SymbolDiagnostics(pkg.PkgPath(), m.Key())...)
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
func summarize(v *gate.View, owner dto.Package, sym dto.Symbol) string {
	if sig, ok := v.Signature(owner.PkgPath(), sym.Key()); ok {
		return sig
	}
	if src, ok := v.SpecSource(owner.PkgPath(), sym.Key()); ok {
		for _, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) == 0 || strings.HasPrefix(trimmed, "//") {
				continue
			}
			return strings.TrimRight(trimmed, " \t{")
		}
	}
	return sym.Kind().String() + " " + sym.Key()
}
