package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

func describePackage(eng *engine.Engine) mcp.ToolHandlerFor[DescribePackageInput, DescribePackageOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribePackageInput) (*mcp.CallToolResult, DescribePackageOutput, error) {
		var out DescribePackageOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *engine.View, pkg engine.Package) error {
			if doc := pkg.Doc(); doc != "" {
				out.Doc = new(string)
				*out.Doc = doc
			}
			for _, f := range pkg.Files() {
				out.Files = append(out.Files, f.Path().Base())
			}
			out.DiagBlock = diagBlock(v.Diagnostics(pkg.PkgPath()))
			return nil
		})
		return nil, out, err
	}
}

func describeFile(eng *engine.Engine) mcp.ToolHandlerFor[DescribeFileInput, DescribeFileOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeFileInput) (*mcp.CallToolResult, DescribeFileOutput, error) {
		var out DescribeFileOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *engine.View, pkg engine.Package) error {
			name, err := fileArg(v.Module(), pkg.PkgPath(), in.FileName)
			if err != nil {
				return err
			}
			var target *engine.File
			for _, f := range pkg.Files() {
				if f.Path().Base() == name {
					target = &f
					break
				}
			}
			if target == nil {
				return fmt.Errorf("no file %q in package %q", name, in.PkgPath)
			}
			if doc := target.Doc(); doc != "" {
				out.Doc = new(string)
				*out.Doc = doc
			}
			out.DiagBlock = diagBlock(diagsForFile(v.Diagnostics(pkg.PkgPath()), target.Path()))
			return nil
		})
		return nil, out, err
	}
}

func describeSymbol(eng *engine.Engine) mcp.ToolHandlerFor[DescribeSymbolInput, DescribeSymbolOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeSymbolInput) (*mcp.CallToolResult, DescribeSymbolOutput, error) {
		var out DescribeSymbolOutput
		err := readSymbol(ctx, eng, in.PkgPath, in.SymbolKey, func(v *engine.View, sym engine.Symbol, owner engine.Package) error {
			src, ok := v.DeclSource(owner.PkgPath(), sym.Key())
			if !ok {
				return fmt.Errorf("source extraction failed for %q", in.SymbolKey)
			}
			out.File = sym.File().Base()
			out.Source = src
			out.Kind = sym.Kind().String()
			diags := v.SymbolDiagnostics(owner.PkgPath(), sym.Key())
			if sym.Kind() == engine.KindType {
				out.Methods = methodSignatures(v, owner, sym.Key())
				for _, m := range v.Methods(owner, sym.Key()) {
					diags = append(diags, v.SymbolDiagnostics(owner.PkgPath(), m.Key())...)
				}
			}
			out.DiagBlock = diagBlock(diags)
			return nil
		})
		return nil, out, err
	}
}
