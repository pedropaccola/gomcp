package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/engine"
	"github.com/pedropaccola/gomcp/internal/gate"
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
		err := eng.Read(ctx, func(v *gate.View) error {
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
		err = eng.Read(ctx, func(v *gate.View) error {
			out.Matches = newMatchEntries(v.SymbolsRegexp(re))
			return nil
		})
		return nil, out, err
	}
}

func searchImplementors(eng *engine.Engine) mcp.ToolHandlerFor[SearchImplementorsInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchImplementorsInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		search := func() error {
			return eng.Read(ctx, func(v *gate.View) error {
				_, owner, err := resolveSymbol(v, in.PkgPath, in.SymbolKey, dto.KindType)
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
		}
		err := search()
		// A narrowly-checked generation (Recheck v2) can't answer this scan
		// safely — force the one full recheck this needs and retry, rather
		// than pay it on every edit for every other read.
		if errors.Is(err, gate.ErrNarrowlyChecked) {
			if fullErr := eng.EnsureFullyChecked(ctx); fullErr != nil {
				return nil, out, fullErr
			}
			err = search()
		}
		return nil, out, err
	}
}

func searchReferences(eng *engine.Engine) mcp.ToolHandlerFor[SearchReferencesInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchReferencesInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := eng.Read(ctx, func(v *gate.View) error {
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
func resolveAnySymbol(v *gate.View, addr, key string) (dto.Symbol, dto.Package, error) {
	pkg, err := canonicalizePkg(v.Module(), addr)
	if err != nil {
		return dto.Symbol{}, dto.Package{}, err
	}
	if sym, owner, ok := v.Symbol(pkg, key); ok {
		return sym, owner, nil
	}
	if clean, ok := address.CleanPath(addr); ok {
		if _, cached := v.ExternalPackage(address.PkgPath(clean)); cached {
			return dto.Symbol{}, dto.Package{}, fmt.Errorf("%q is a dependency: its API is served read-only by list_* and describe_*; semantic search stays in the workspace", addr)
		}
	}
	return dto.Symbol{}, dto.Package{}, fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, addr)
}

// resolveSymbol is resolveAnySymbol plus kind checking.
func resolveSymbol(v *gate.View, dir, key string, want dto.SymbolKind) (dto.Symbol, dto.Package, error) {
	sym, owner, err := resolveAnySymbol(v, dir, key)
	if err != nil {
		return dto.Symbol{}, dto.Package{}, err
	}
	if sym.Kind() != want {
		return dto.Symbol{}, dto.Package{}, fmt.Errorf("%q is a %s, not a %s: use the matching describe_* tool", key, sym.Kind(), want)
	}
	return sym, owner, nil
}

// newMatchEntries renders scan hits for the search_* outputs: canonical
// package address, key, kind.
func newMatchEntries(matches []dto.Match) []MatchEntry {
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
