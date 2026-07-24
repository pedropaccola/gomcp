package tools

import (
	"fmt"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
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
func newDiagnosticEntry(d engine.Diagnostic) DiagnosticEntry {
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
		*e.FileName = string(d.File)
	}
	if d.Key != "" {
		e.SymbolKey = new(string)
		*e.SymbolKey = d.Key
	}
	return e
}

// canonPkg canonicalizes an agent-supplied package address against the
// workspace module: module-prefixed addresses pass through, bare workspace
// directories gain the prefix. File names are refused — packages are
// directories, always spelled alone.
func canonPkg(module address.PkgPath, addr string) (address.PkgPath, error) {
	path, ok := address.CleanPath(addr)
	if !ok {
		return "", fmt.Errorf("invalid package path %q", addr)
	}
	if strings.HasSuffix(path.String(), ".go") {
		return "", fmt.Errorf("%q names a file; package arguments take the package alone", addr)
	}
	if path == "." || address.PkgPath(path) == module {
		return module, nil
	}
	if strings.HasPrefix(path.String(), module.String()+"/") {
		return address.PkgPath(path), nil
	}
	return address.PkgPath(module.String() + "/" + path.String()), nil
}

// fileArg normalizes an agent-supplied file address inside pkg: a bare
// *.go name, or a path accepted when its package agrees — spelled raw
// (dependency and canonical workspace addresses) or workspace-relative.
// Contradictions are refused, never guessed.
func fileArg(module, pkg address.PkgPath, file string) (string, error) {
	if strings.Contains(file, "/") {
		fpath, ok := address.CleanPath(file)
		if !ok {
			return "", fmt.Errorf("invalid file path %q", file)
		}
		if address.PkgPath(fpath.Dir()) != pkg {
			canon, err := canonPkg(module, fpath.Dir().String())
			if err != nil || canon != pkg {
				return "", fmt.Errorf("file %q does not live in package %q", file, pkg)
			}
		}
		file = fpath.Base()
	}
	if !strings.HasSuffix(file, ".go") {
		return "", fmt.Errorf("file name must be a bare *.go name, got %q", file)
	}
	return file, nil
}

// pkgAddr composes the canonical address of a workspace directory.
func pkgAddr(module address.PkgPath, dir address.RelativePath) string {
	if dir == "." {
		return module.String()
	}
	return module.String() + "/" + dir.String()
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
func diagsForFile(diags []engine.Diagnostic, path address.RelativePath) []engine.Diagnostic {
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
func newDiagnosticsTruncated(diags []engine.Diagnostic, limit int) DiagnosticsTruncated {
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
