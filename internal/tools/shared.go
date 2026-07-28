package tools

import (
	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
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
func newDiagnosticEntry(d dto.Diagnostic) DiagnosticEntry {
	e := DiagnosticEntry{
		Kind:    d.Kind.String(),
		Message: d.Msg,
	}
	if d.Package != "" {
		e.PkgPath = new(string)
		*e.PkgPath = string(d.Package)
	}
	if d.File != "" {
		e.FileName = new(string)
		*e.FileName = d.File.Base()
	}
	if d.Key != "" {
		e.SymbolKey = new(string)
		*e.SymbolKey = d.Key
	}
	return e
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
// tool addresses a symbol: PkgPath/SymbolKey are directly usable as-is with
// describe_symbol/edit_symbol. FileName is the coarser fallback when a
// diagnostic is attributable to a file but no single declaration; all three
// are nil for module/driver-level problems.
type DiagnosticEntry struct {
	PkgPath   *string `json:"pkg_path,omitempty"`
	FileName  *string `json:"file_name,omitempty"`
	SymbolKey *string `json:"symbol_key,omitempty"`
	Kind      string  `json:"kind"`
	Message   string  `json:"message"`
}

// diagsForFile narrows a package's diagnostics down to one file's own.
func diagsForFile(diags []dto.Diagnostic, path address.FilePath) []dto.Diagnostic {
	out := diags[:0:0]
	for _, d := range diags {
		if d.File == path {
			out = append(out, d)
		}
	}
	return out
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
func newDiagnosticsTruncated(diags []dto.Diagnostic, limit int) DiagnosticsTruncated {
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
