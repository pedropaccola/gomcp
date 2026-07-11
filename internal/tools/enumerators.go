package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

func listPackages(eng *engine.Engine) mcp.ToolHandlerFor[ListPackagesInput, ListPackagesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListPackagesInput) (*mcp.CallToolResult, ListPackagesOutput, error) {
		var out ListPackagesOutput
		err := eng.Read(func(v *engine.View) error {
			last := ""
			for _, pkg := range v.Packages() {
				if addr := pkgAddr(v.Module(), pkg.Path()); addr != last {
					out.Packages = append(out.Packages, addr)
					last = addr
				}
			}
			out.DiagBlock = diagBlock(v.WorkspaceDiagnostics())
			return nil
		})
		return nil, out, err
	}
}

func listFiles(eng *engine.Engine) mcp.ToolHandlerFor[ListFilesInput, ListFilesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListFilesInput) (*mcp.CallToolResult, ListFilesOutput, error) {
		var out ListFilesOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *engine.View, pkg engine.Package) error {
			for _, file := range pkg.Files() {
				out.Files = append(out.Files, file.Path().Base())
			}
			out.DiagBlock = diagBlock(v.Diagnostics(pkg.PkgPath()))
			return nil
		})
		return nil, out, err
	}
}

func listSymbols(eng *engine.Engine) mcp.ToolHandlerFor[ListSymbolsInput, ListSymbolsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListSymbolsInput) (*mcp.CallToolResult, ListSymbolsOutput, error) {
		var out ListSymbolsOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *engine.View, pkg engine.Package) error {
			var target *engine.File
			if fileName := optStr(in.FileName); fileName != "" {
				name, err := fileArg(v.Module(), pkg.PkgPath(), fileName)
				if err != nil {
					return err
				}
				for _, f := range pkg.Files() {
					if f.Path().Base() == name {
						target = &f
						break
					}
				}
				if target == nil {
					return fmt.Errorf("no file %q in package %q", name, in.PkgPath)
				}
			}
			for _, sym := range pkg.Symbols() {
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
			out.DiagBlock = diagBlock(diags)
			return nil
		})
		return nil, out, err
	}
}

func listMethods(eng *engine.Engine) mcp.ToolHandlerFor[ListMethodsInput, ListMethodsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListMethodsInput) (*mcp.CallToolResult, ListMethodsOutput, error) {
		var out ListMethodsOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *engine.View, pkg engine.Package) error {
			out.Methods = methodSignatures(v, pkg, in.SymbolKey)
			var diags []engine.Diagnostic
			for _, m := range v.Methods(pkg, in.SymbolKey) {
				diags = append(diags, v.SymbolDiagnostics(pkg.PkgPath(), m.Key())...)
			}
			out.DiagBlock = diagBlock(diags)
			return nil
		})
		return nil, out, err
	}
}

// summarize renders a symbol's one-line summary: the signature for funcs and
// methods, the trimmed first declaration line — doc comment skipped — for
// everything else.
func summarize(v *engine.View, owner engine.Package, sym engine.Symbol) string {
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
