package tools

import (
	"github.com/pedropaccola/gomcp/internal/store"
)

// toolConfig holds process-wide tool configuration set once at Register
// time — currently just diagLimit, threaded into every handler that caps
// diagnostics instead of living as a package-level var. Replaces a former
// diagLimit global plus a SetDiagLimit setter: that shape was safe only by
// convention (SetDiagLimit had to run before Register, unenforced by the
// type system), whereas this shape makes the value immutable for the
// server's whole lifetime by construction.
type toolConfig struct {
	diagLimit int
}

// newDiagnosticEntry renders one diagnostic into its wire-facing shape.
func newDiagnosticEntry(d store.Diagnostic) DiagnosticEntry {
	fileName := ""
	if d.File != "" {
		fileName = d.File.Base()
	}
	return DiagnosticEntry{
		PkgPath:   string(d.Package),
		FileName:  fileName,
		SymbolKey: d.Key,
		Kind:      d.Kind.String(),
		Message:   d.Msg,
	}
}

// DiagnosticsTruncated is the shared optional diagnostics view, scoped to whatever the
// carrying tool read. See the package doc's output convention. Diagnostics
// is capped at diagLimit (default 20, tunable via -diagnostics-limit);
// Truncated is nil when everything fit, otherwise the count left out —
// the diagnostics tool itself is never capped, so it's always the
// complete-inventory fallback.
type DiagnosticsTruncated struct {
	Diagnostics []DiagnosticEntry `json:"diagnostics,omitempty"`
	Truncated   *int              `json:"truncated,omitempty"`
}

// DiagnosticEntry is one problem report, addressed the same way every other
// tool addresses a symbol: PkgPath is always present. SymbolKey and
// FileName are the two ways attribution gets coarser, each omitted when a
// diagnostic can't be pinned that precisely — SymbolKey to no single
// declaration (the common case), FileName (rarer) to no workspace file at
// all.
type DiagnosticEntry struct {
	PkgPath   string `json:"pkg_path"`
	FileName  string `json:"file_name,omitempty"`
	SymbolKey string `json:"symbol_key,omitempty"`
	Kind      string `json:"kind"`
	Message   string `json:"message"`
}

// DirectiveChange reports directive lines a single edit added or removed,
// relative to the target's state before the edit — never emitted by a
// create, since there's nothing yet to compare against. SymbolKey is
// empty for a file-level directive change (edit_files), populated for a
// symbol-level one (edit_symbols); unlike DiagnosticEntry, FileName is
// never omitted — a directive change is always attributable to exactly
// one file, whichever level it was made at.
type DirectiveChange struct {
	PkgPath   string   `json:"pkg_path"`
	FileName  string   `json:"file_name"`
	SymbolKey string   `json:"symbol_key,omitempty"`
	Added     []string `json:"added,omitempty"`
	Removed   []string `json:"removed,omitempty"`
}

// optStr collapses an optional input pointer to its plain value — nil (the
// field was omitted) and a pointer to "" (the field was explicitly sent
// empty) are treated identically as "not given," matching every optional
// field's documented default.
func optStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// newToolConfig builds a toolConfig; a negative diagLimit is ignored in
// favor of the default (20) — there is no such thing as showing fewer than
// zero diagnostics.
func newToolConfig(diagLimit int) *toolConfig {
	if diagLimit < 0 {
		diagLimit = 20
	}
	return &toolConfig{diagLimit: diagLimit}
}

// newDiagnosticsTruncated converts and caps diags to at most limit
// entries, returning the view: the entries shown and, when any were cut,
// how many.
func newDiagnosticsTruncated(diags []store.Diagnostic, limit int) DiagnosticsTruncated {
	if len(diags) == 0 {
		return DiagnosticsTruncated{}
	}
	shown := diags
	if len(diags) > limit {
		shown = diags[:limit]
	}
	entries := make([]DiagnosticEntry, len(shown))
	for i, d := range shown {
		entries[i] = newDiagnosticEntry(d)
	}
	block := DiagnosticsTruncated{Diagnostics: entries}
	if len(diags) > limit {
		block.Truncated = new(len(diags) - limit)
	}
	return block
}

// newDirectiveChange renders one directive delta into its wire-facing
// shape.
func newDirectiveChange(d store.DirectiveDelta) DirectiveChange {
	return DirectiveChange{
		PkgPath:   string(d.Package),
		FileName:  d.File.Base(),
		SymbolKey: d.Key,
		Added:     d.Added,
		Removed:   d.Removed,
	}
}
