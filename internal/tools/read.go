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
				if addr := pkgAddr(v.Module(), pkg.Path); addr != last {
					out.Packages = append(out.Packages, addr)
					last = addr
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
		err := readPackage(ctx, eng, in.Package, func(v *engine.View, pkg *engine.Package) error {
			for _, file := range v.Files(pkg) {
				out.Files = append(out.Files, file.Path.Base())
			}
			out.Diagnostics = diagStrings(v.Diagnostics(pkg.PkgPath))
			return nil
		})
		return nil, out, err
	}
}

func listSymbols(eng *engine.Engine) mcp.ToolHandlerFor[ListSymbolsInput, ListSymbolsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListSymbolsInput) (*mcp.CallToolResult, ListSymbolsOutput, error) {
		var out ListSymbolsOutput
		err := readPackage(ctx, eng, in.Package, func(v *engine.View, pkg *engine.Package) error {
			var target *engine.File
			if in.File != "" {
				name, err := fileArg(v.Module(), pkg.PkgPath, in.File)
				if err != nil {
					return err
				}
				for _, f := range v.Files(pkg) {
					if f.Path.Base() == name {
						target = f
						break
					}
				}
				if target == nil {
					return fmt.Errorf("no file %q in package %q", name, in.Package)
				}
			}
			for _, sym := range v.Symbols(pkg) {
				if target != nil && sym.File != target.Path {
					continue
				}
				out.Symbols = append(out.Symbols, SymbolEntry{
					Key:     sym.Key(),
					Kind:    sym.Kind.String(),
					Summary: summarize(v, sym),
				})
			}
			if target != nil {
				out.Diagnostics = diagStrings(target.Diags)
			} else {
				out.Diagnostics = diagStrings(v.Diagnostics(pkg.PkgPath))
			}
			return nil
		})
		return nil, out, err
	}
}

func listMethods(eng *engine.Engine) mcp.ToolHandlerFor[ListMethodsInput, ListMethodsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListMethodsInput) (*mcp.CallToolResult, ListMethodsOutput, error) {
		var out ListMethodsOutput
		err := readPackage(ctx, eng, in.Package, func(v *engine.View, pkg *engine.Package) error {
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
		err := readSymbol(ctx, eng, in.Package, in.Name, func(v *engine.View, sym *engine.Symbol, owner *engine.Package) error {
			if sym.Kind != engine.KindType {
				return fmt.Errorf("%q is a %s, not a type: use the matching describe_* tool", in.Name, sym.Kind)
			}
			src, ok := v.DeclSource(sym)
			if !ok {
				return fmt.Errorf("source extraction failed for %q", in.Name)
			}
			out.File = sym.File.Base()
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
		return describeDecl(ctx, eng, in.Package, in.Name, engine.KindFunc)
	}
}

func describeMethod(eng *engine.Engine) mcp.ToolHandlerFor[DescribeMethodInput, DescribeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DescribeMethodInput) (*mcp.CallToolResult, DescribeOutput, error) {
		return describeDecl(ctx, eng, in.Package, in.Type+"."+in.Name, engine.KindMethod)
	}
}

func describeDecl(ctx context.Context, eng *engine.Engine, addr, key string, kind engine.SymbolKind) (*mcp.CallToolResult, DescribeOutput, error) {
	var out DescribeOutput
	err := readSymbol(ctx, eng, addr, key, func(v *engine.View, sym *engine.Symbol, _ *engine.Package) error {
		if sym.Kind != kind {
			return fmt.Errorf("%q is a %s, not a %s: use the matching describe_* tool", key, sym.Kind, kind)
		}
		src, ok := v.DeclSource(sym)
		if !ok {
			return fmt.Errorf("source extraction failed for %q", key)
		}
		out.File = sym.File.Base()
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

// readPackage resolves a package address across both worlds and runs fn
// under the read gate with the resolved package: workspace first, then the
// dependency cache, lazily loading the dependency on a workspace miss —
// loads never happen under the gate.
func readPackage(ctx context.Context, eng *engine.Engine, addr string, fn func(*engine.View, *engine.Package) error) error {
	canon, err := canonPkg(eng.ModulePath(), addr)
	if err != nil {
		return err
	}
	clean, cleanOK := engine.CleanPath(addr)
	ext := engine.PkgPath(clean)
	extOK := cleanOK && ext != canon && ext != "."
	attempt := func() (bool, error) {
		found := false
		err := eng.Read(func(v *engine.View) error {
			if pkg, ok := v.Package(canon); ok {
				found = true
				return fn(v, pkg)
			}
			if extOK {
				if pkg, ok := v.ExternalPackage(ext); ok {
					found = true
					return fn(v, pkg)
				}
			}
			return nil
		})
		return found, err
	}
	if found, err := attempt(); err != nil || found {
		return err
	}
	if !extOK {
		return fmt.Errorf("no package at %q: call list_packages for valid addresses", addr)
	}
	if err := eng.LoadExternal(ctx, ext); err != nil {
		return fmt.Errorf("no workspace package at %q, and %v", addr, err)
	}
	if found, err := attempt(); err != nil || found {
		return err
	}
	return fmt.Errorf("no package at %q", addr)
}

// readSymbol is readPackage plus symbol resolution: workspace units fall
// through Prod into XTest; dependency packages resolve their exported
// index directly.
func readSymbol(ctx context.Context, eng *engine.Engine, addr, key string, fn func(*engine.View, *engine.Symbol, *engine.Package) error) error {
	return readPackage(ctx, eng, addr, func(v *engine.View, pkg *engine.Package) error {
		if sym, owner, ok := v.Symbol(pkg.PkgPath, key); ok {
			return fn(v, sym, owner)
		}
		if sym, ok := pkg.Symbols[key]; ok {
			return fn(v, sym, pkg)
		}
		return fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, addr)
	})
}

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

// resolveAnySymbol resolves a workspace package address and symbol key —
// the semantic finders' gate: dependencies are excluded, since their type
// universe cannot be matched exactly against the workspace's.
func resolveAnySymbol(v *engine.View, addr, key string) (*engine.Symbol, *engine.Package, error) {
	pkg, err := canonPkg(v.Module(), addr)
	if err != nil {
		return nil, nil, err
	}
	if sym, owner, ok := v.Symbol(pkg, key); ok {
		return sym, owner, nil
	}
	if clean, ok := engine.CleanPath(addr); ok {
		if _, cached := v.ExternalPackage(engine.PkgPath(clean)); cached {
			return nil, nil, fmt.Errorf("%q is a dependency: its API is served read-only by list_* and describe_*; semantic search stays in the workspace", addr)
		}
	}
	return nil, nil, fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, addr)
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
			Package: m.Pkg.PkgPath.String(),
			Key:     m.Sym.Key(),
			Kind:    m.Sym.Kind.String(),
		})
	}
	return out
}
