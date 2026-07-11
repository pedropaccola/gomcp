package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

// Mutation tool implementations, in the same semantic sections as the
// engine's mutation layer: Creators, Editors, Refactorings, Session. Every
// handler is one Engine.Edit transaction relayed through runEdit; shapes
// live in tools.go. Handlers carry no doc comments by design — they are
// mechanical relays, documented by their tool descriptions in Register.

// runEdit is the composite every mutating handler flows through: one
// transaction, echoed as files changed plus the diagnostics delta.
func runEdit(ctx context.Context, eng *engine.Engine, fn func(*engine.Tx) error) (*mcp.CallToolResult, MutationOutput, error) {
	var out MutationOutput
	report, err := eng.Edit(ctx, fn)
	if err != nil {
		return nil, out, err
	}
	out.Files = filesByPackage(eng.ModulePath(), report.Changed)
	out.Diagnostics = diagStrings(report.Delta)
	out.Resolved = diagStrings(report.Resolved)
	if report.Stale {
		out.RecheckUnavailable = true
		out.Diagnostics = []string{"recheck unavailable, diagnostics stale: " + report.Note}
	}
	return nil, out, nil
}

// ----- Creators -----

func createPackage(eng *engine.Engine) mcp.ToolHandlerFor[CreatePackageInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreatePackageInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreatePackage(pkg, in.Name)
		})
	}
}

func createFile(eng *engine.Engine) mcp.ToolHandlerFor[CreateFileInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateFileInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(eng.ModulePath(), pkg, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreateFile(pkg, file)
		})
	}
}

func createDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[CreateDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(eng.ModulePath(), pkg, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreateSymbol(pkg, file, in.Source)
		})
	}
}

// ----- Editors -----

func editDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[EditDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.ReplaceSymbol(pkg, in.Key, in.Source)
		})
	}
}

func deleteDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[DeleteDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeleteSymbol(pkg, in.Key)
		})
	}
}

func deleteFile(eng *engine.Engine) mcp.ToolHandlerFor[DeleteFileInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(eng.ModulePath(), pkg, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeleteFile(pkg, file)
		})
	}
}

func deletePackage(eng *engine.Engine) mcp.ToolHandlerFor[DeletePackageInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeletePackageInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeletePackage(pkg)
		})
	}
}

// ----- Refactorings -----

func moveDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[MoveDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MoveDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(eng.ModulePath(), pkg, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.MoveSymbol(pkg, in.Key, file)
		})
	}
}

func renameDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[RenameDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RenameDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.RenameSymbol(pkg, in.Key, in.NewName)
		})
	}
}

func renameFile(eng *engine.Engine) mcp.ToolHandlerFor[RenameFileInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RenameFileInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(eng.ModulePath(), pkg, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.RenameFile(pkg, file, in.NewName)
		})
	}
}

func renamePackage(eng *engine.Engine) mcp.ToolHandlerFor[RenamePackageInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RenamePackageInput) (*mcp.CallToolResult, MutationOutput, error) {
		pkg, err := packageArg(eng, in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		newPkg, err := packageArg(eng, in.NewPath)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.RenamePackage(pkg, newPkg)
		})
	}
}

// ----- Session -----

func flush(eng *engine.Engine) mcp.ToolHandlerFor[FlushInput, FlushOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ FlushInput) (*mcp.CallToolResult, FlushOutput, error) {
		written, removed, err := eng.Flush()
		module := eng.ModulePath()
		return nil, FlushOutput{
			Written: filesByPackage(module, written),
			Removed: filesByPackage(module, removed),
		}, err
	}
}

func reload(eng *engine.Engine) mcp.ToolHandlerFor[ReloadInput, ReloadOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ReloadInput) (*mcp.CallToolResult, ReloadOutput, error) {
		var out ReloadOutput
		discarded, err := eng.Reload(ctx)
		if err != nil {
			return nil, out, err
		}
		out.Discarded = filesByPackage(eng.ModulePath(), discarded)
		err = eng.Read(func(v *engine.View) error {
			out.Diagnostics = diagStrings(v.AllDiagnostics())
			return nil
		})
		return nil, out, err
	}
}

// ----- helpers -----

// packageArg validates and canonicalizes a package address for the
// mutation handlers — the write-side gate: dependencies are refused, the
// workspace is the only mutable world.
func packageArg(eng *engine.Engine, addr string) (engine.PkgPath, error) {
	canon, err := canonPkg(eng.ModulePath(), addr)
	if err != nil {
		return "", err
	}
	if clean, ok := engine.CleanPath(addr); ok && eng.IsExternal(engine.PkgPath(clean)) {
		return "", fmt.Errorf("dependency %q is read-only", addr)
	}
	return canon, nil
}

// filesByPackage groups workspace-relative paths into the interface's
// address convention: canonical package address to bare file names. Input
// order is preserved within each package, so sorted paths stay sorted.
func filesByPackage(module engine.PkgPath, paths []engine.RelativePath) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for _, p := range paths {
		key := pkgAddr(module, p.Dir())
		out[key] = append(out[key], p.Base())
	}
	return out
}
