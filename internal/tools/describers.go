package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
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
	DiagnosticsTruncated
}

// DescribeFileEntry addresses one file to describe.
type DescribeFileEntry struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
}

type DescribeFileResult struct {
	Doc *string `json:"doc,omitempty"`
	DiagnosticsTruncated
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
	DiagnosticsTruncated
}

func describePackage(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DescribePackageInput, DescribePackageOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribePackageInput) (*mcp.CallToolResult, DescribePackageOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribePackageOutput{}, errEmptyBatch("describes")
		}
		n := len(in.Describes)
		out := DescribePackageOutput{Results: make([]DescribePackageResult, n)}
		for i, entry := range in.Describes {
			err := readPackage(ctx, eng, entry.PkgPath, func(v *store.View, pkg workspace.PackageID) error {
				res := &out.Results[i]
				if doc, _ := v.PackageDoc(pkg); doc != "" {
					res.Doc = new(string)
					*res.Doc = doc
				}
				files, _ := v.PackageFiles(pkg)
				res.Files = make([]string, 0, len(files))
				for _, f := range files {
					res.Files = append(res.Files, f.Base())
				}
				res.DiagnosticsTruncated = newDiagnosticsTruncated(v.Diagnostics(pkg.Base()), cfg.diagLimit)
				return nil
			})
			if err != nil {
				return nil, DescribePackageOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}

func describeFile(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DescribeFileInput, DescribeFileOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeFileInput) (*mcp.CallToolResult, DescribeFileOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribeFileOutput{}, errEmptyBatch("describes")
		}
		n := len(in.Describes)
		out := DescribeFileOutput{Results: make([]DescribeFileResult, n)}
		for i, entry := range in.Describes {
			err := readFile(ctx, eng, entry.PkgPath, entry.FileName, func(v *store.View, fp workspace.FilePath, pkg workspace.PackageID) error {
				res := &out.Results[i]
				if doc, ok := v.FileDoc(fp); ok && doc != "" {
					res.Doc = new(string)
					*res.Doc = doc
				}
				res.DiagnosticsTruncated = newDiagnosticsTruncated(diagsForFile(v.Diagnostics(pkg.Base()), fp), cfg.diagLimit)
				return nil
			})
			if err != nil {
				return nil, DescribeFileOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}

func describeSymbol(eng *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DescribeSymbolInput, DescribeSymbolOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeSymbolInput) (*mcp.CallToolResult, DescribeSymbolOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribeSymbolOutput{}, errEmptyBatch("describes")
		}
		n := len(in.Describes)
		out := DescribeSymbolOutput{Results: make([]DescribeSymbolResult, n)}
		for i, entry := range in.Describes {
			err := readSymbol(ctx, eng, entry.PkgPath, entry.SymbolKey, func(v *store.View, sym store.Symbol, owner workspace.PackageID) error {
				src, ok := v.DeclSource(owner.Base(), sym.Key)
				if !ok {
					return fmt.Errorf("source extraction failed for %q", entry.SymbolKey)
				}
				res := &out.Results[i]
				res.File = sym.File.Base()
				res.Source = src
				res.Kind = sym.Kind
				diags := v.SymbolDiagnostics(owner.Base(), sym.Key)
				if sym.IsType() {
					res.Methods = methodSignatures(v, owner, sym.Key)
					for _, m := range v.Methods(owner, sym.Key) {
						diags = append(diags, v.SymbolDiagnostics(owner.Base(), m.Key)...)
					}
				}
				res.DiagnosticsTruncated = newDiagnosticsTruncated(diags, cfg.diagLimit)
				return nil
			})
			if err != nil {
				return nil, DescribeSymbolOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}
