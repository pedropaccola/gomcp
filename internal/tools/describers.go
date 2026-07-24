package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

// DescribeFileInput is one or more files to describe, in one round trip,
// resolved in order.
type DescribeFileInput struct {
	Describes []DescribeFileEntry `json:"describes"`
}

// DescribeFileOutput is each entry's result, in the same order as
// Describes.
type DescribeFileOutput struct {
	Results []DescribeFileResult `json:"results"`
}

// DescribePackageInput is one or more packages to describe, in one round
// trip, resolved in order.
type DescribePackageInput struct {
	Describes []DescribePackageEntry `json:"describes"`
}

// DescribePackageOutput is each entry's result, in the same order as
// Describes.
type DescribePackageOutput struct {
	Results []DescribePackageResult `json:"results"`
}

// DescribeSymbolInput is one or more symbols to describe, in one round
// trip, resolved in order.
type DescribeSymbolInput struct {
	Describes []DescribeSymbolEntry `json:"describes"`
}

// DescribeSymbolOutput is each entry's result, in the same order as
// Describes.
type DescribeSymbolOutput struct {
	Results []DescribeSymbolResult `json:"results"`
}

// DescribeSymbolEntry addresses one symbol to describe.
type DescribeSymbolEntry struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

// DescribeSymbolResult covers every symbol kind uniformly; Methods is only
// populated when Kind == "type".
type DescribeSymbolResult struct {
	File    string   `json:"file"`
	Source  string   `json:"source"`
	Kind    string   `json:"kind"`
	Methods []string `json:"methods,omitempty"`
	DiagBlock
}

// DescribeFileEntry addresses one file to describe.
type DescribeFileEntry struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
}

type DescribeFileResult struct {
	Doc *string `json:"doc,omitempty"`
	DiagBlock
}

// DescribePackageEntry addresses one package to describe.
type DescribePackageEntry struct {
	PkgPath string `json:"pkg_path"`
}

// DescribePackageResult is the package's godoc plus the file list already
// on hand while assembling it.
type DescribePackageResult struct {
	Doc   *string  `json:"doc,omitempty"`
	Files []string `json:"files,omitempty"`
	DiagBlock
}

func describePackage(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[DescribePackageInput, DescribePackageOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribePackageInput) (*mcp.CallToolResult, DescribePackageOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribePackageOutput{}, fmt.Errorf("describes must not be empty")
		}
		n := len(in.Describes)
		out := DescribePackageOutput{Results: make([]DescribePackageResult, n)}
		for i, entry := range in.Describes {
			err := readPackage(ctx, eng, entry.PkgPath, func(v *engine.View, pkg engine.Package) error {
				res := &out.Results[i]
				if doc := pkg.Doc(); doc != "" {
					res.Doc = new(string)
					*res.Doc = doc
				}
				files := pkg.Files()
				res.Files = make([]string, 0, len(files))
				for _, f := range files {
					res.Files = append(res.Files, f.Path().Base())
				}
				res.DiagBlock = cfg.diagBlock(v.Diagnostics(pkg.PkgPath()))
				return nil
			})
			if err != nil {
				return nil, DescribePackageOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}

func describeFile(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[DescribeFileInput, DescribeFileOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeFileInput) (*mcp.CallToolResult, DescribeFileOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribeFileOutput{}, fmt.Errorf("describes must not be empty")
		}
		n := len(in.Describes)
		out := DescribeFileOutput{Results: make([]DescribeFileResult, n)}
		for i, entry := range in.Describes {
			err := readPackage(ctx, eng, entry.PkgPath, func(v *engine.View, pkg engine.Package) error {
				name, err := fileArg(v.Module(), pkg.PkgPath(), entry.FileName)
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
					return fmt.Errorf("no file %q in package %q", name, entry.PkgPath)
				}
				res := &out.Results[i]
				if doc := target.Doc(); doc != "" {
					res.Doc = new(string)
					*res.Doc = doc
				}
				res.DiagBlock = cfg.diagBlock(diagsForFile(v.Diagnostics(pkg.PkgPath()), target.Path()))
				return nil
			})
			if err != nil {
				return nil, DescribeFileOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}

func describeSymbol(eng *engine.Engine, cfg *toolConfig) mcp.ToolHandlerFor[DescribeSymbolInput, DescribeSymbolOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeSymbolInput) (*mcp.CallToolResult, DescribeSymbolOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribeSymbolOutput{}, fmt.Errorf("describes must not be empty")
		}
		n := len(in.Describes)
		out := DescribeSymbolOutput{Results: make([]DescribeSymbolResult, n)}
		for i, entry := range in.Describes {
			err := readSymbol(ctx, eng, entry.PkgPath, entry.SymbolKey, func(v *engine.View, sym engine.Symbol, owner engine.Package) error {
				src, ok := v.DeclSource(owner.PkgPath(), sym.Key())
				if !ok {
					return fmt.Errorf("source extraction failed for %q", entry.SymbolKey)
				}
				res := &out.Results[i]
				res.File = sym.File().Base()
				res.Source = src
				res.Kind = sym.Kind().String()
				diags := v.SymbolDiagnostics(owner.PkgPath(), sym.Key())
				if sym.Kind() == engine.KindType {
					res.Methods = methodSignatures(v, owner, sym.Key())
					for _, m := range v.Methods(owner, sym.Key()) {
						diags = append(diags, v.SymbolDiagnostics(owner.PkgPath(), m.Key())...)
					}
				}
				res.DiagBlock = cfg.diagBlock(diags)
				return nil
			})
			if err != nil {
				return nil, DescribeSymbolOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}
