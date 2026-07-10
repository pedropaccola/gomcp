// Package state is the engine's trusted core: the vocabulary types of the
// in-memory model (addresses, symbols, files, packages, diagnostics) and
// the Workspace that owns them. The model's hot fields are unexported and
// change only through named primitives — SwapFile and AddLoadedFile are
// the only doors for file content, and every enumerating accessor is
// sorted-only. Everything above this package (engine verbs, lookups,
// tools) composes what it exports; the documented invariants hold here by
// construction, not by convention.
package state
