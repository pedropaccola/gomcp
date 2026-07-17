package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

type DescribeFileInput struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
}

type DescribeFileOutput struct {
	Doc *string `json:"doc,omitempty"`
	DiagBlock
}

type DescribePackageInput struct {
	PkgPath string `json:"pkg_path"`
}

// DescribePackageOutput is the package's godoc plus the file list already
// on hand while assembling it.
type DescribePackageOutput struct {
	Doc   *string  `json:"doc,omitempty"`
	Files []string `json:"files,omitempty"`
	DiagBlock
}

type DescribeSymbolInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

// DescribeSymbolOutput covers every symbol kind uniformly; Methods is only
// populated when Kind == "type".
type DescribeSymbolOutput struct {
	File    string   `json:"file"`
	Source  string   `json:"source"`
	Kind    string   `json:"kind"`
	Methods []string `json:"methods,omitempty"`
	DiagBlock
}

func describePackage(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[DescribePackageInput, DescribePackageOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribePackageInput) (*mcp.CallToolResult, DescribePackageOutput, error) {
		var out DescribePackageOutput
		err := readPackage(ctx, eng, in.PkgPath, func(v *engine.View, pkg engine.Package) error {
			if doc := pkg.Doc(); doc != "" {
				out.Doc = new(string)
				*out.Doc = doc
			}
			files := pkg.Files()
			out.Files = make([]string, 0, len(files))
			for _, f := range files {
				out.Files = append(out.Files, f.Path().Base())
			}
			out.DiagBlock = cfg.diagBlock(v.Diagnostics(pkg.PkgPath()))
			return nil
		})
		return nil, out, err
	}
}

func describeFile(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[DescribeFileInput, DescribeFileOutput] {
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
			out.DiagBlock = cfg.diagBlock(diagsForFile(v.Diagnostics(pkg.PkgPath()), target.Path()))
			return nil
		})
		return nil, out, err
	}
}

func describeSymbol(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[DescribeSymbolInput, DescribeSymbolOutput] {
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
			out.DiagBlock = cfg.diagBlock(diags)
			return nil
		})
		return nil, out, err
	}
}
