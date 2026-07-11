package tools

import (
	"fmt"
	"strings"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
)

// diagLimit caps the diagnostics rendered in every scoped DiagBlock —
// list_* output, describe_* output, and mutation echoes — so a
// wide-blast-radius read or edit can't drown the agent in text; the
// diagnostics tool remains the uncapped inventory. SetDiagLimit overrides
// the default once at startup, ahead of Register.
var diagLimit = 20

// diagEntry renders one diagnostic into its wire-facing shape.
func diagEntry(d engine.Diagnostic) DiagnosticEntry {
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

// SetDiagLimit overrides diagLimit (see its doc); call before Register.
// Negative n is ignored — there is no such thing as showing fewer than
// zero diagnostics.
func SetDiagLimit(n int) {
	if n >= 0 {
		diagLimit = n
	}
}

// diagBlock renders diagnostics into a DiagBlock, capped to diagLimit —
// the read-side shape, embedded directly (never nil: an empty DiagBlock's
// fields already omit independently, so there's no wrapping key to hide).
func diagBlock(diags []engine.Diagnostic) DiagBlock {
	if len(diags) == 0 {
		return DiagBlock{}
	}
	shown := diags
	if len(diags) > diagLimit {
		shown = diags[:diagLimit]
	}
	entries := make([]DiagnosticEntry, len(shown))
	for i, d := range shown {
		entries[i] = diagEntry(d)
	}
	block := DiagBlock{Diagnostics: entries}
	if len(diags) > diagLimit {
		block.Truncated = new(len(diags) - diagLimit)
	}
	return block
}

// diagBlockPtr is diagBlock's write-side counterpart: nil when there's
// nothing to report, so a named field carrying it (WriteOutput) omits the
// whole object instead of delivering an empty one.
func diagBlockPtr(diags []engine.Diagnostic) *DiagBlock {
	if len(diags) == 0 {
		return nil
	}
	block := diagBlock(diags)
	return &block
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
