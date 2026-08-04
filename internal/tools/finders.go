package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

type MatchEntry struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
	FileName  string `json:"file_name"`
	Kind      string `json:"kind"`
}

type SearchImplementorsInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
	FileName  string `json:"file_name"`
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
	FileName  string `json:"file_name"`
}

type SearchSourceInput struct {
	Regexp string `json:"regexp"`
}

func searchDeclarationsLike(st *store.Store) mcp.ToolHandlerFor[SearchLikeInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchLikeInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := st.Read(ctx, func(v *store.View) error {
			out.Matches = newMatchEntries(v.SymbolsLike(in.Name))
			return nil
		})
		return nil, out, err
	}
}

func searchSource(st *store.Store) mcp.ToolHandlerFor[SearchSourceInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchSourceInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		re, err := regexp.Compile(in.Regexp)
		if err != nil {
			return nil, out, fmt.Errorf("invalid regular expression: %w", err)
		}
		err = st.Read(ctx, func(v *store.View) error {
			out.Matches = newMatchEntries(v.SymbolsRegexp(re))
			return nil
		})
		return nil, out, err
	}
}

func searchImplementors(st *store.Store) mcp.ToolHandlerFor[SearchImplementorsInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchImplementorsInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		search := func() error {
			return st.Read(ctx, func(v *store.View) error {
				owner, err := v.ResolveType(in.PkgPath, in.SymbolKey, in.FileName)
				if err != nil {
					return err
				}
				matches, err := v.SymbolsImplementing(owner.Base(), in.SymbolKey)
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
		if errors.Is(err, store.ErrNarrowlyChecked) {
			if fullErr := st.EnsureFullyChecked(ctx); fullErr != nil {
				return nil, out, fullErr
			}
			err = search()
		}
		return nil, out, err
	}
}

func searchReferences(st *store.Store) mcp.ToolHandlerFor[SearchReferencesInput, SearchOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchReferencesInput) (*mcp.CallToolResult, SearchOutput, error) {
		var out SearchOutput
		err := st.Read(ctx, func(v *store.View) error {
			sym, err := resolveAnySymbol(v, in.PkgPath, in.SymbolKey, in.FileName)
			if err != nil {
				return err
			}
			matches, err := v.SymbolsReferencing(sym.Owner.Base(), in.SymbolKey)
			if err != nil {
				return err
			}
			out.Matches = newMatchEntries(matches)
			return nil
		})
		return nil, out, err
	}
}

// resolveAnySymbol resolves a workspace package address and symbol key,
// scoped to fileName when non-empty (an assertion, never a hint, same
// convention as describe/edit/delete_symbols) — the semantic finders'
// gate: dependencies are excluded, since their type universe cannot be
// matched exactly against the workspace's.
func resolveAnySymbol(v *store.View, addr, key, fileName string) (store.Symbol, error) {
	pkg, err := workspace.NewPackagePath(v.Module(), addr)
	if err != nil {
		return store.Symbol{}, err
	}
	var sym store.Symbol
	var ok bool
	if fileName != "" {
		sym, ok = v.SymbolIn(pkg, key, fileName)
	} else {
		sym, ok = v.Symbol(pkg, key)
	}
	if ok {
		return sym, nil
	}
	if v.HasExternalPackage(workspace.PackagePath(addr)) {
		return store.Symbol{}, fmt.Errorf("%q is a dependency: its API is served read-only by list_* and describe_*; semantic search stays in the workspace", addr)
	}
	return store.Symbol{}, fmt.Errorf("%s: call list_symbols for valid keys", workspace.NoSymbolError(key, addr))
}

// newMatchEntries renders scan hits for the search_* outputs: canonical
// package address, key, file, kind.
func newMatchEntries(matches []store.Symbol) []MatchEntry {
	out := make([]MatchEntry, 0, len(matches))
	for _, m := range matches {
		out = append(out, MatchEntry{
			PkgPath:   m.Owner.String(),
			SymbolKey: m.Key,
			FileName:  m.File.Base(),
			Kind:      m.Kind,
		})
	}
	return out
}
