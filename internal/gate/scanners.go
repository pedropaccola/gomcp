package gate

import (
	"errors"
	"regexp"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// ErrNarrowlyChecked mirrors workspace.ErrNarrowlyChecked at the gate
// boundary, so a caller outside workspace's own ACL (internal/tools) can
// detect it without importing workspace directly.
var ErrNarrowlyChecked = errors.New("workspace was narrowly rechecked: SymbolsImplementing needs a full recheck first")

// SymbolsImplementing scans for named types whose value or pointer method
// set satisfies the given interface symbol, checked with full type
// information — embedding and promoted methods included. Returns
// ErrNarrowlyChecked if the current generation mixes packages from two
// different type-checking sessions (Recheck v2) — the one analysis that
// can't safely answer without a full recheck first.
func (v *View) SymbolsImplementing(pkg address.PkgPath, key string) ([]dto.Match, error) {
	matches, err := v.ws.SymbolsImplementing(v.ctx, pkg, key)
	if errors.Is(err, workspace.ErrNarrowlyChecked) {
		return nil, ErrNarrowlyChecked
	}
	if err != nil {
		return nil, err
	}
	return v.toMatches(matches), nil
}

// SymbolsLike scans for symbols whose key contains substr, case-insensitively.
func (v *View) SymbolsLike(substr string) []dto.Match {
	return v.toMatches(v.ws.SymbolsLike(v.ctx, substr))
}

// SymbolsReferencing scans every package's resolved identifier uses for
// references to the given symbol and reports the enclosing declarations, in
// the same address space as every other scanner. The definition itself and
// self-references (recursion) are excluded. Matching is by qualified name —
// (import path, receiver, name) — which is exact for Go and immune to the
// duplicate type-checked instances that test variants create.
func (v *View) SymbolsReferencing(pkg address.PkgPath, key string) ([]dto.Match, error) {
	matches, err := v.ws.SymbolsReferencing(v.ctx, pkg, key)
	if err != nil {
		return nil, err
	}
	return v.toMatches(matches), nil
}

// SymbolsRegexp scans each symbol's own source and collects the symbols
// whose text matches re — the general-purpose matcher for when neither key
// nor name can identify the target. It searches the in-memory truth, so
// unsaved mutations are visible to it. Text outside keyed declarations
// (package clauses, imports, init bodies, floating comments) is not
// addressable by symbol and therefore not searched.
func (v *View) SymbolsRegexp(re *regexp.Regexp) []dto.Match {
	return v.toMatches(v.ws.SymbolsRegexp(v.ctx, re))
}

// toMatches translates workspace's address-only scan hits into dto's own
// shape.
func (v *View) toMatches(ms []workspace.SymbolMatch) []dto.Match {
	out := make([]dto.Match, 0, len(ms))
	for _, m := range ms {
		sym, pkg, ok := v.Symbol(m.Pkg, m.Key)
		if !ok {
			continue
		}
		out = append(out, dto.Match{Pkg: pkg, Sym: sym})
	}
	return out
}
