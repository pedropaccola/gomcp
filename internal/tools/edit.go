package tools

import (
	"context"
	"fmt"
	"strings"

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
	out.Files = filesByPackage(report.Changed)
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
		dir, err := packageArg(in.Package)
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
		file, err := fileArg(dir, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreateFile(dir, file)
		})
	}
}

func createDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[CreateDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CreateDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(dir, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.CreateSymbol(dir, file, in.Source)
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
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(dir, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.DeleteFile(dir.Join(file))
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

func moveDeclaration(eng *engine.Engine) mcp.ToolHandlerFor[MoveDeclarationInput, MutationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in MoveDeclarationInput) (*mcp.CallToolResult, MutationOutput, error) {
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(dir, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.MoveSymbol(dir, in.Key, file)
		})
	}
}

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
		dir, err := packageArg(in.Package)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		file, err := fileArg(dir, in.File)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		return runEdit(ctx, eng, func(tx *engine.Tx) error {
			return tx.RenameFile(dir.Join(file), in.NewName)
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
		written, removed, err := eng.Flush()
		return nil, FlushOutput{
			Written: filesByPackage(written),
			Removed: filesByPackage(removed),
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
		out.Discarded = filesByPackage(discarded)
		err = eng.Read(func(v *engine.View) error {
			out.Diagnostics = diagStrings(v.AllDiagnostics())
			return nil
		})
		return nil, out, err
	}
}

// ----- helpers -----

// fileArg normalizes an agent-supplied file address inside dir: a bare
// *.go name, or a workspace-relative path accepted when its directory
// agrees with dir. Contradictions are refused, never guessed.
func fileArg(dir engine.RelativePath, file string) (string, error) {
	if strings.Contains(file, "/") {
		fpath, ok := engine.CleanPath(file)
		if !ok {
			return "", fmt.Errorf("invalid file path %q: must be workspace-relative", file)
		}
		if fpath.Dir() != dir {
			return "", fmt.Errorf("file %q does not live in package %q", file, dir)
		}
		file = fpath.Base()
	}
	if !strings.HasSuffix(file, ".go") {
		return "", fmt.Errorf("file name must be a bare *.go name, got %q", file)
	}
	return file, nil
}

// packageArg validates an agent-supplied package address: a clean,
// workspace-relative directory path. File names are refused — packages
// are directories, always spelled alone.
func packageArg(dir string) (engine.RelativePath, error) {
	path, ok := engine.CleanPath(dir)
	if !ok {
		return "", fmt.Errorf("invalid package path %q: must be workspace-relative", dir)
	}
	if strings.HasSuffix(path.String(), ".go") {
		return "", fmt.Errorf("%q names a file; package arguments take the directory alone", dir)
	}
	return path, nil
}

// filesByPackage groups workspace-relative paths into the interface's
// address convention: package directory to bare file names. Input order is
// preserved within each package, so sorted paths stay sorted.
func filesByPackage(paths []engine.RelativePath) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for _, p := range paths {
		dir := p.Dir().String()
		out[dir] = append(out[dir], p.Base())
	}
	return out
}
