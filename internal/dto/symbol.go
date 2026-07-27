package dto

import "github.com/pedropaccola/gomcp/internal/address"

// Symbol is dto's read-only view of one addressable top-level
// declaration: the key, kind, receiver, and owning file — constructed
// directly by workspace's own read API, so it carries no AST pointer and
// is safe to hold past the Read/Edit closure that produced it. Content
// (source text, diagnostics) is fetched separately, by address, through
// View's Source and Diagnostics methods.
type Symbol struct {
	key  string
	kind SymbolKind
	recv string
	file address.FilePath
}

// File is the workspace-relative path of the file that declares the symbol.
func (s Symbol) File() address.FilePath { return s.file }

// Key is the symbol's address within its package: "Recv.Name" for methods,
// "Name" otherwise.
func (s Symbol) Key() string { return s.key }

// Kind classifies the declaration.
func (s Symbol) Kind() SymbolKind { return s.kind }

// Recv is the receiver type name; empty except for methods.
func (s Symbol) Recv() string { return s.recv }

// NewSymbol constructs a Symbol from its plain fields.
func NewSymbol(key string, kind SymbolKind, recv string, file address.FilePath) Symbol {
	return Symbol{key: key, kind: kind, recv: recv, file: file}
}
