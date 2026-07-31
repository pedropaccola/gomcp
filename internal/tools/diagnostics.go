package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

type DiagnosticsInput struct{}

type DiagnosticsOutput struct {
	Diagnostics []DiagnosticEntry `json:"diagnostics"`
}

// DiagnosticsPackageEntry addresses one package to report diagnostics for.
type DiagnosticsPackageEntry struct {
	PkgPath string `json:"pkg_path"`
}

// DiagnosticsPackagesInput batches DiagnosticsPackageEntry addresses.
type DiagnosticsPackagesInput struct {
	Diagnoses []DiagnosticsPackageEntry `json:"diagnoses"`
}

// DiagnosticsPackagesOutput is each entry's diagnostics, in the same
// order as Diagnoses.
type DiagnosticsPackagesOutput struct {
	Results []DiagnosticsTruncated `json:"results"`
}

// DiagnosticsFileEntry addresses one file to report diagnostics for.
type DiagnosticsFileEntry struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
}

// DiagnosticsFilesInput batches DiagnosticsFileEntry addresses.
type DiagnosticsFilesInput struct {
	Diagnoses []DiagnosticsFileEntry `json:"diagnoses"`
}

// DiagnosticsFilesOutput is each entry's diagnostics, in the same order
// as Diagnoses.
type DiagnosticsFilesOutput struct {
	Results []DiagnosticsTruncated `json:"results"`
}

// DiagnosticsSymbolEntry addresses one symbol to report diagnostics for.
type DiagnosticsSymbolEntry struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

// DiagnosticsSymbolsInput batches DiagnosticsSymbolEntry addresses — the
// one diagnostics_* tool whose entries may span different packages, since
// each is already individually addressed.
type DiagnosticsSymbolsInput struct {
	Diagnoses []DiagnosticsSymbolEntry `json:"diagnoses"`
}

// DiagnosticsSymbolsOutput is each entry's diagnostics, in the same
// order as Diagnoses.
type DiagnosticsSymbolsOutput struct {
	Results []DiagnosticsTruncated `json:"results"`
}

func diagnostics(st *store.Store) mcp.ToolHandlerFor[DiagnosticsInput, DiagnosticsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ DiagnosticsInput) (*mcp.CallToolResult, DiagnosticsOutput, error) {
		var out DiagnosticsOutput
		err := st.Read(ctx, func(v *store.View) error {
			diags := v.AllDiagnostics()
			out.Diagnostics = make([]DiagnosticEntry, len(diags))
			for i, diag := range diags {
				out.Diagnostics[i] = newDiagnosticEntry(diag)
			}
			return nil
		})
		return nil, out, err
	}
}

func diagnosticsPackages(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DiagnosticsPackagesInput, DiagnosticsPackagesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DiagnosticsPackagesInput) (*mcp.CallToolResult, DiagnosticsPackagesOutput, error) {
		if len(in.Diagnoses) == 0 {
			return nil, DiagnosticsPackagesOutput{}, errEmptyBatch("diagnoses")
		}
		n := len(in.Diagnoses)
		out := DiagnosticsPackagesOutput{Results: make([]DiagnosticsTruncated, n)}
		for i, entry := range in.Diagnoses {
			err := readPackage(ctx, st, entry.PkgPath, func(v *store.View, pkg workspace.PackageID) error {
				out.Results[i] = newDiagnosticsTruncated(v.Diagnostics(pkg.Base()), cfg.diagLimit)
				return nil
			})
			if err != nil {
				return nil, DiagnosticsPackagesOutput{}, batchErr("diagnoses", i, n, err)
			}
		}
		return nil, out, nil
	}
}

func diagnosticsFiles(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DiagnosticsFilesInput, DiagnosticsFilesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DiagnosticsFilesInput) (*mcp.CallToolResult, DiagnosticsFilesOutput, error) {
		if len(in.Diagnoses) == 0 {
			return nil, DiagnosticsFilesOutput{}, errEmptyBatch("diagnoses")
		}
		n := len(in.Diagnoses)
		out := DiagnosticsFilesOutput{Results: make([]DiagnosticsTruncated, n)}
		for i, entry := range in.Diagnoses {
			err := readFile(ctx, st, entry.PkgPath, entry.FileName, func(v *store.View, fp workspace.FilePath, pkg workspace.PackageID) error {
				out.Results[i] = newDiagnosticsTruncated(v.FileDiagnostics(pkg.Base(), fp), cfg.diagLimit)
				return nil
			})
			if err != nil {
				return nil, DiagnosticsFilesOutput{}, batchErr("diagnoses", i, n, err)
			}
		}
		return nil, out, nil
	}
}

func diagnosticsSymbols(st *store.Store, cfg *toolConfig) mcp.ToolHandlerFor[DiagnosticsSymbolsInput, DiagnosticsSymbolsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DiagnosticsSymbolsInput) (*mcp.CallToolResult, DiagnosticsSymbolsOutput, error) {
		if len(in.Diagnoses) == 0 {
			return nil, DiagnosticsSymbolsOutput{}, errEmptyBatch("diagnoses")
		}
		n := len(in.Diagnoses)
		out := DiagnosticsSymbolsOutput{Results: make([]DiagnosticsTruncated, n)}
		for i, entry := range in.Diagnoses {
			err := readSymbol(ctx, st, entry.PkgPath, entry.SymbolKey, func(v *store.View, sym store.Symbol, owner workspace.PackageID) error {
				out.Results[i] = newDiagnosticsTruncated(v.SymbolDiagnostics(owner.Base(), sym.Key), cfg.diagLimit)
				return nil
			})
			if err != nil {
				return nil, DiagnosticsSymbolsOutput{}, batchErr("diagnoses", i, n, err)
			}
		}
		return nil, out, nil
	}
}
