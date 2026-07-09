package tools

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

// Read tool implementations, in the same semantic sections as the engine's
// lookup layer: Enumerators, Describers, Finders, Diagnostics. Every handler
// is one Engine.Read scope composing lookups; shapes live in tools.go.

// ----- Enumerators -----

func listPackages(eng *engine.Engine) mcp.ToolHandlerFor[ListPackagesInput, ListPackagesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListPackagesInput) (*mcp.CallToolResult, ListPackagesOutput, error) {
		var out ListPackagesOutput
		err := eng.Read(func(v *engine.View) error {
			last := ""
			for _, pkg := range v.Packages() {
				if p := pkg.Path.String(); p != last {
					out.Packages = append(out.Packages, p)
					last = p
				}
			}
			out.Diagnostics = diagStrings(v.WorkspaceDiagnostics())
			return nil
		})
		return nil, out, err
	}
}

func listFiles(eng *engine.Engine) mcp.ToolHandlerFor[ListFilesInput, ListFilesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListFilesInput) (*mcp.CallToolResult, ListFilesOutput, error) {
		var out ListFilesOutput
		err := eng.Read(func(v *engine.View) error {
			pkg, err := resolvePackage(v, in.Package)
			if err != nil {
				return err
			}
			for _, file := range v.Files(pkg) {
				out.Files = append(out.Files, file.Path.String())
			}
			out.Diagnostics = diagStrings(v.Diagnostics(pkg.Path))
			return nil
		})
		return nil, out, err
	}
}

func listSymbols(eng *engine.Engine) mcp.ToolHandlerFor[ListSymbolsInput, ListSymbolsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListSymbolsInput) (*mcp.CallToolResult, ListSymbolsOutput, error) {
		var out ListSymbolsOutput
		err := eng.Read(func(v *engine.View) error {
			pkg, err := resolvePackage(v, in.Package)
			if err != nil {
				return err
			}
			var fileFilter engine.RelativePath
			if in.File != "" {
				name, err := fileArg(pkg.Path, in.File)
				if err != nil {
					return err
				}
				fileFilter = pkg.Path.Join(name)
			}
			for _, sym := range v.Symbols(pkg) {
				if fileFilter != "" && sym.File != fileFilter {
					continue
				}
				out.Symbols = append(out.Symbols, SymbolEntry{
					Key:     sym.Key(),
					Kind:    sym.Kind.String(),
					Summary: summarize(v, sym),
				})
			}
			if fileFilter != "" {
				if file, _, ok := v.File(fileFilter); ok {
					out.Diagnostics = diagStrings(file.Diags)
				}
			} else {
				out.Diagnostics = diagStrings(v.Diagnostics(pkg.Path))
			}
			return nil
		})
		return nil, out, err
	}
}

func listMethods(eng *engine.Engine) mcp.ToolHandlerFor[ListMethodsInput, ListMethodsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListMethodsInput) (*mcp.CallToolResult, ListMethodsOutput, error) {
		var out ListMethodsOutput
		err := eng.Read(func(v *engine.View) error {
			pkg, err := resolvePackage(v, in.Package)
			if err != nil {
				return err
			}
			out.Methods = methodSignatures(v, pkg, in.Type)
			for _, m := range v.Methods(pkg, in.Type) {
				out.Diagnostics = append(out.Diagnostics, diagStrings(v.SymbolDiagnostics(m))...)
			}
			return nil
		})
		return nil, out, err
	}
}

// ----- Describers -----

func describeType(eng *engine.Engine) mcp.ToolHandlerFor[DescribeTypeInput, DescribeTypeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeTypeInput) (*mcp.CallToolResult, DescribeTypeOutput, error) {
		var out DescribeTypeOutput
		err := eng.Read(func(v *engine.View) error {
			sym, owner, err := resolveSymbol(v, in.Package, in.Name, engine.KindType)
			if err != nil {
				return err
			}
			src, ok := v.DeclSource(sym)
			if !ok {
				return fmt.Errorf("source extraction failed for %q", in.Name)
			}
			out.File = sym.File.String()
			out.Source = string(src)
			out.Methods = methodSignatures(v, owner, in.Name)
			out.Diagnostics = diagStrings(v.SymbolDiagnostics(sym))
			for _, m := range v.Methods(owner, in.Name) {
				out.Diagnostics = append(out.Diagnostics, diagStrings(v.SymbolDiagnostics(m))...)
			}
			return nil
		})
		return nil, out, err
	}
}

func describeFunction(eng *engine.Engine) mcp.ToolHandlerFor[DescribeFunctionInput, DescribeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeFunctionInput) (*mcp.CallToolResult, DescribeOutput, error) {
		return describeDecl(eng, in.Package, in.Name, engine.KindFunc)
	}
}

func describeMethod(eng *engine.Engine) mcp.ToolHandlerFor[DescribeMethodInput, DescribeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeMethodInput) (*mcp.CallToolResult, DescribeOutput, error) {
		return describeDecl(eng, in.Package, in.Type+"."+in.Name, engine.KindMethod)
	}
}

func describeDecl(eng *engine.Engine, dir, key string, kind engine.SymbolKind) (*mcp.CallToolResult, DescribeOutput, error) {
	var out DescribeOutput
	err := eng.Read(func(v *engine.View) error {
		sym, _, err := resolveSymbol(v, dir, key, kind)
		if err != nil {
			return err
		}
		src, ok := v.DeclSource(sym)
		if !ok {
			return fmt.Errorf("source extraction failed for %q", key)
		}
		out.File = sym.File.String()
		out.Source = string(src)
		out.Diagnostics = diagStrings(v.SymbolDiagnostics(sym))
		return nil
	})
	return nil, out, err
}

// ----- Finders -----

func searchDeclarationsLike(eng *engine.Engine) mcp.ToolHandlerFor[SearchLikeInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchLikeInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := eng.Read(func(v *engine.View) error {
			out.Matches = matchEntries(v.SymbolsLike(in.Name))
			return nil
		})
		return nil, out, err
	}
}

func searchSource(eng *engine.Engine) mcp.ToolHandlerFor[SearchSourceInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchSourceInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		re, err := regexp.Compile(in.Regexp)
		if err != nil {
			return nil, out, fmt.Errorf("invalid regular expression: %w", err)
		}
		err = eng.Read(func(v *engine.View) error {
			out.Matches = matchEntries(v.SymbolsRegexp(re))
			return nil
		})
		return nil, out, err
	}
}

func searchImplementors(eng *engine.Engine) mcp.ToolHandlerFor[SearchImplementorsInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchImplementorsInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := eng.Read(func(v *engine.View) error {
			sym, _, err := resolveSymbol(v, in.Package, in.Name, engine.KindType)
			if err != nil {
				return err
			}
			matches, err := v.SymbolsImplementing(sym)
			if err != nil {
				return err
			}
			out.Matches = matchEntries(matches)
			return nil
		})
		return nil, out, err
	}
}

func searchReferences(eng *engine.Engine) mcp.ToolHandlerFor[SearchReferencesInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchReferencesInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := eng.Read(func(v *engine.View) error {
			sym, _, err := resolveAnySymbol(v, in.Package, in.Key)
			if err != nil {
				return err
			}
			matches, err := v.SymbolsReferencing(sym)
			if err != nil {
				return err
			}
			out.Matches = matchEntries(matches)
			return nil
		})
		return nil, out, err
	}
}

// ----- Diagnostics -----

func diagnostics(eng *engine.Engine) mcp.ToolHandlerFor[DiagnosticsInput, DiagnosticsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ DiagnosticsInput) (*mcp.CallToolResult, DiagnosticsOutput, error) {
		var out DiagnosticsOutput
		err := eng.Read(func(v *engine.View) error {
			diags := v.AllDiagnostics()
			out.Diagnostics = make([]string, 0, len(diags))
			for _, diag := range diags {
				out.Diagnostics = append(out.Diagnostics, diag.String())
			}
			return nil
		})
		return nil, out, err
	}
}

// ----- Shared helpers -----

// diagStrings renders diagnostics for a DiagBlock; nil when empty so that
// omitempty drops the block entirely on healthy scopes.
func diagStrings(diags []engine.Diagnostic) []string {
	if len(diags) == 0 {
		return nil
	}
	out := make([]string, len(diags))
	for i, diag := range diags {
		out[i] = diag.String()
	}
	return out
}

// resolvePackage is the shared address gate: it validates untrusted path
// input through the same rules as every package argument and resolves it
// to the production package.
func resolvePackage(v *engine.View, dir string) (*engine.Package, error) {
	path, err := packageArg(dir)
	if err != nil {
		return nil, err
	}
	pkg, ok := v.Package(path)
	if !ok {
		return nil, fmt.Errorf("no package at %q: call list_packages for valid addresses", dir)
	}
	return pkg, nil
}

// resolveAnySymbol resolves a package path and symbol key to the symbol and
// its owning package, any kind.
func resolveAnySymbol(v *engine.View, dir, key string) (*engine.Symbol, *engine.Package, error) {
	path, ok := engine.CleanPath(dir)
	if !ok {
		return nil, nil, fmt.Errorf("invalid package path %q: must be workspace-relative", dir)
	}
	sym, owner, ok := v.Symbol(path, key)
	if !ok {
		return nil, nil, fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, dir)
	}
	return sym, owner, nil
}

// resolveSymbol is resolveAnySymbol plus kind checking.
func resolveSymbol(v *engine.View, dir, key string, want engine.SymbolKind) (*engine.Symbol, *engine.Package, error) {
	sym, owner, err := resolveAnySymbol(v, dir, key)
	if err != nil {
		return nil, nil, err
	}
	if sym.Kind != want {
		return nil, nil, fmt.Errorf("%q is a %s, not a %s: use the matching describe_* tool", key, sym.Kind, want)
	}
	return sym, owner, nil
}

// summarize renders a symbol's one-line summary: the signature for funcs and
// methods, the trimmed first declaration line — doc comment skipped — for
// everything else.
func summarize(v *engine.View, sym *engine.Symbol) string {
	if sig, ok := v.Signature(sym); ok {
		return string(sig)
	}
	if src, ok := v.SpecSource(sym); ok {
		for _, line := range bytes.Split(src, []byte("\n")) {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 || bytes.HasPrefix(trimmed, []byte("//")) {
				continue
			}
			return strings.TrimRight(string(trimmed), " \t{")
		}
	}
	return sym.Kind.String() + " " + sym.Key()
}

func methodSignatures(v *engine.View, pkg *engine.Package, typeName string) []string {
	var out []string
	for _, m := range v.Methods(pkg, typeName) {
		if sig, ok := v.Signature(m); ok {
			out = append(out, string(sig))
		}
	}
	return out
}

func matchEntries(matches []engine.Match) []MatchEntry {
	out := make([]MatchEntry, 0, len(matches))
	for _, m := range matches {
		out = append(out, MatchEntry{
			Package: m.Pkg.Path.String(),
			Key:     m.Sym.Key(),
			Kind:    m.Sym.Kind.String(),
		})
	}
	return out
}
