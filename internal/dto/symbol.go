package dto

import "github.com/pedropaccola/gomcp/internal/address"

// Symbol is dto's read-only view of one addressable top-level
// declaration: the key, kind, receiver, and owning file — constructed
// directly by workspace's own read API, so it carries no AST pointer and
// is safe to hold past the Read/Edit closure that produced it. Content
// (source text, diagnostics) is fetched separately, by address, through
// View's Source and Diagnostics methods.
type Symbol struct {
	Key  string
	Kind SymbolKind
	Recv string
	File address.FilePath
}
