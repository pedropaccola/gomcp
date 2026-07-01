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
// live in tools.go.

// runEdit is the composite every mutating handler flows through: one
// transaction, echoed as files changed plus the diagnostics delta.
func runEdit(ctx context.Context, eng *engine.Engine, fn func(*engine.Tx) error) (*mcp.CallToolResult, MutationOutput, error) {
	var out MutationOutput
	report, err := eng.Edit(ctx, fn)
	if err != nil {
		return nil, out, err
	}
	for _, path := range report.Changed {
		out.Files = append(out.Files, path.String())
	}
	out.Diagnostics = diagStrings(report.Delta)
	out.Resolved = diagStrings(report.Resolved)
	if report.Stale {
		out.RecheckUnavailable = true
		out.Diagnostics = []string{"recheck unavailable, diagnostics stale: " + report.Note}
	}
	return nil, out, nil
}

// packageArg validates an agent-supplied package path.
func packageArg(dir string) (engine.RelativePath, error) {
	path, ok := engine.CleanPath(dir)
	if !ok {
		return "", fmt.Errorf("invalid package path %q: must be workspace-relative", dir)
	}
	return path, nil
}

// ----- Creators -----

func createPackage(eng *engine.Engine) mcp.ToolHandlerFor[CreatePackageInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreatePackageInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Path)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreatePackage(dir, in.Name)
		})
	}
}

func createFile(eng *engine.Engine) mcp.ToolHandlerFor[CreateFileInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateFileInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreateFile(dir, in.Name)
		})
	}
}

func createDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[CreateDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreateSymbol(dir, in.File, in.Source)
		})
	}
}

// ----- Editors -----

func editDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[EditDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in EditDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.ReplaceSymbol(dir, in.Key, in.Source)
		})
	}
}

func deleteDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[DeleteDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeleteSymbol(dir, in.Key)
		})
	}
}

func deleteFile(eng *engine.Engine) mcp.ToolHandlerFor[DeleteFileInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, MutationOutput, error) {
		path, err := packageArg(in.Path)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeleteFile(path)
		})
	}
}

func deletePackage(eng *engine.Engine) mcp.ToolHandlerFor[DeletePackageInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DeletePackageInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeletePackage(dir)
		})
	}
}

// ----- Refactorings -----

func renameDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[RenameDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RenameDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.RenameSymbol(dir, in.Key, in.NewName)
		})
	}
}

func renameFile(eng *engine.Engine) mcp.ToolHandlerFor[RenameFileInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RenameFileInput) (*mcp.CallToolResult, MutationOutput, error) {
		path, err := packageArg(in.Path)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.RenameFile(path, in.NewName)
		})
	}
}

func renamePackage(eng *engine.Engine) mcp.ToolHandlerFor[RenamePackageInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RenamePackageInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		newDir, err := packageArg(in.NewPath)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.RenamePackage(dir, newDir)
		})
	}
}

// ----- Session -----

func flush(eng *engine.Engine) mcp.ToolHandlerFor[FlushInput, FlushOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ FlushInput) (*mcp.CallToolResult, FlushOutput, error) {
		var out FlushOutput
		written, removed, err := eng.Flush()
		for _, path := range written {
			out.Written = append(out.Written, path.String())
		}
		for _, path := range removed {
			out.Removed = append(out.Removed, path.String())
		}
		return nil, out, err
	}
}
