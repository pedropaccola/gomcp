package tools

import (
	"context"
	"fmt"
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
)

type MatchEntry struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
	Kind      string `json:"kind"`
}

type SearchImplementorsInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

type SearchLikeInput struct {
	Name string `json:"name"`
}

type SearchOutput struct {
	Matches []MatchEntry `json:"matches"`
}

type SearchReferencesInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

type SearchSourceInput struct {
	Regexp string `json:"regexp"`
}

func searchDeclarationsLike(eng *engine.Engine) mcp.ToolHandlerFor[SearchLikeInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchLikeInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := eng.Read(ctx, func(v *engine.View) error {
			out.Matches = newMatchEntries(v.SymbolsLike(in.Name))
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
		err = eng.Read(ctx, func(v *engine.View) error {
			out.Matches = newMatchEntries(v.SymbolsRegexp(re))
			return nil
		})
		return nil, out, err
	}
}

func searchImplementors(eng *engine.Engine) mcp.ToolHandlerFor[SearchImplementorsInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchImplementorsInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := eng.Read(ctx, func(v *engine.View) error {
			_, owner, err := resolveSymbol(v, in.PkgPath, in.SymbolKey, engine.KindType)
			if err != nil {
				return err
			}
			matches, err := v.SymbolsImplementing(owner.PkgPath(), in.SymbolKey)
			if err != nil {
				return err
			}
			out.Matches = newMatchEntries(matches)
			return nil
		})
		return nil, out, err
	}
}

func searchReferences(eng *engine.Engine) mcp.ToolHandlerFor[SearchReferencesInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchReferencesInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := eng.Read(ctx, func(v *engine.View) error {
			_, owner, err := resolveAnySymbol(v, in.PkgPath, in.SymbolKey)
			if err != nil {
				return err
			}
			matches, err := v.SymbolsReferencing(owner.PkgPath(), in.SymbolKey)
			if err != nil {
				return err
			}
			out.Matches = newMatchEntries(matches)
			return nil
		})
		return nil, out, err
	}
}

// resolveAnySymbol resolves a workspace package address and symbol key —
// the semantic finders' gate: dependencies are excluded, since their type
// universe cannot be matched exactly against the workspace's.
func resolveAnySymbol(v *engine.View, addr, key string) (engine.Symbol, engine.Package, error) {
	pkg, err := canonPkg(v.Module(), addr)
	if err != nil {
		return engine.Symbol{}, engine.Package{}, err
	}
	if sym, owner, ok := v.Symbol(pkg, key); ok {
		return sym, owner, nil
	}
	if clean, ok := address.CleanPath(addr); ok {
		if _, cached := v.ExternalPackage(address.PkgPath(clean)); cached {
			return engine.Symbol{}, engine.Package{}, fmt.Errorf("%q is a dependency: its API is served read-only by list_* and describe_*; semantic search stays in the workspace", addr)
		}
	}
	return engine.Symbol{}, engine.Package{}, fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, addr)
}

// resolveSymbol is resolveAnySymbol plus kind checking.
func resolveSymbol(v *engine.View, dir, key string, want engine.SymbolKind) (engine.Symbol, engine.Package, error) {
	sym, owner, err := resolveAnySymbol(v, dir, key)
	if err != nil {
		return engine.Symbol{}, engine.Package{}, err
	}
	if sym.Kind() != want {
		return engine.Symbol{}, engine.Package{}, fmt.Errorf("%q is a %s, not a %s: use the matching describe_* tool", key, sym.Kind(), want)
	}
	return sym, owner, nil
}

// newMatchEntries renders scan hits for the search_* outputs: canonical
// package address, key, kind.
func newMatchEntries(matches []engine.Match) []MatchEntry {
	out := make([]MatchEntry, 0, len(matches))
	for _, m := range matches {
		out = append(out, MatchEntry{
			PkgPath:   m.Pkg.PkgPath().String(),
			SymbolKey: m.Sym.Key(),
			Kind:      m.Sym.Kind().String(),
		})
	}
	return out
}
