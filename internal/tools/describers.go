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
	FileName  string `json:"file_name"`
}

// DescribeSymbolResult covers every symbol kind uniformly; Methods is only
// populated when Kind == "type". PackageKind is omitted for the common
// Prod case, present only when the symbol resolved into an XTest member.
// Ignored and Generated are independent, orthogonal signals — either can
// land on either shape — each omitted when false. No File field: the
// caller already supplied file_name, and resolution is scoped exactly to
// it, so echoing it back would only repeat what the caller gave us.
type DescribeSymbolResult struct {
	Source      string   `json:"source"`
	Kind        string   `json:"kind"`
	Methods     []string `json:"methods,omitempty"`
	Directives  []string `json:"directives,omitempty"`
	PackageKind string   `json:"package_kind,omitempty"`
	Ignored     bool     `json:"ignored,omitempty"`
	Generated   bool     `json:"generated,omitempty"`
}

// DescribeFileEntry addresses one file to describe.
type DescribeFileEntry struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
}

// DescribeFileResult describes one file's metadata. PackageKind is
// omitted for the common Prod case, present only when the file resolved
// into an XTest member. Ignored and Generated are independent,
// orthogonal signals — either can land on either shape — each omitted
// when false.
type DescribeFileResult struct {
	Doc         *string  `json:"doc,omitempty"`
	Directives  []string `json:"directives,omitempty"`
	PackageKind string   `json:"package_kind,omitempty"`
	Ignored     bool     `json:"ignored,omitempty"`
	Generated   bool     `json:"generated,omitempty"`
}

// DescribePackageEntry addresses one package to describe.
type DescribePackageEntry struct {
	PkgPath string `json:"pkg_path"`
}

// DescribePackageResult is the package's godoc plus the file list already
// on hand while assembling it.
type DescribePackageResult struct {
	Doc   *string     `json:"doc,omitempty"`
	Files []FileEntry `json:"files,omitempty"`
}

func describePackage(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DescribePackageInput, DescribePackageOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribePackageInput) (*mcp.CallToolResult, DescribePackageOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribePackageOutput{}, errEmptyBatch("describes")
		}
		n := len(in.Describes)
		out := DescribePackageOutput{Results: make([]DescribePackageResult, n)}
		for i, entry := range in.Describes {
			err := readPackage(ctx, st, entry.PkgPath, func(v *store.View, pkg workspace.PackagePath) error {
				res := &out.Results[i]
				if doc, _ := v.PackageDoc(pkg); doc != "" {
					res.Doc = new(string)
					*res.Doc = doc
				}
				files, _ := v.PackageFiles(pkg)
				res.Files = make([]FileEntry, 0, len(files))
				for _, f := range files {
					entry := FileEntry{Name: f.Base()}
					entry.PackageKind, _ = v.FileKind(f)
					entry.Ignored, _ = v.FileIgnored(f)
					entry.Generated, _ = v.FileGenerated(f)
					res.Files = append(res.Files, entry)
				}
				return nil
			})
			if err != nil {
				return nil, DescribePackageOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}

func describeFile(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DescribeFileInput, DescribeFileOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeFileInput) (*mcp.CallToolResult, DescribeFileOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribeFileOutput{}, errEmptyBatch("describes")
		}
		n := len(in.Describes)
		out := DescribeFileOutput{Results: make([]DescribeFileResult, n)}
		for i, entry := range in.Describes {
			err := readFile(ctx, st, entry.PkgPath, entry.FileName, func(v *store.View, fp workspace.FilePath, owner workspace.PackageID) error {
				res := &out.Results[i]
				if doc, ok := v.FileDoc(fp); ok && doc != "" {
					res.Doc = new(string)
					*res.Doc = doc
				}
				res.Directives, _ = v.FileDirectives(fp)
				if owner.Kind() != workspace.KindProd {
					res.PackageKind = owner.Kind().String()
				}
				res.Ignored, _ = v.FileIgnored(fp)
				res.Generated, _ = v.FileGenerated(fp)
				return nil
			})
			if err != nil {
				return nil, DescribeFileOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}

func describeSymbol(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DescribeSymbolInput, DescribeSymbolOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeSymbolInput) (*mcp.CallToolResult, DescribeSymbolOutput, error) {
		if len(in.Describes) == 0 {
			return nil, DescribeSymbolOutput{}, errEmptyBatch("describes")
		}
		n := len(in.Describes)
		out := DescribeSymbolOutput{Results: make([]DescribeSymbolResult, n)}
		for i, entry := range in.Describes {
			err := readSymbol(ctx, st, entry.PkgPath, entry.SymbolKey, entry.FileName, func(v *store.View, sym store.Symbol, owner workspace.PackageID) error {
				src, ok := v.DeclSource(owner.Base(), sym.Key, sym.File.Base())
				if !ok {
					return fmt.Errorf("source extraction failed for %q", entry.SymbolKey)
				}
				res := &out.Results[i]
				res.Source = src
				res.Kind = sym.Kind
				res.Directives = sym.Directives
				if owner.Kind() != workspace.KindProd {
					res.PackageKind = owner.Kind().String()
				}
				res.Ignored = sym.Ignored
				res.Generated, _ = v.FileGenerated(sym.File)
				if sym.IsType() {
					res.Methods = methodSignatures(v, owner.Base(), sym.Key)
				}
				return nil
			})
			if err != nil {
				return nil, DescribeSymbolOutput{}, batchErr("describes", i, n, err)
			}
		}
		return nil, out, nil
	}
}
