package tools

import (
	"fmt"
	"strings"

	"github.com/pedropaccola/gomcp/internal/engine"
)

// diagStrings renders diagnostics for a DiagBlock, capped to diagLimit and
// closed with a pointer back to the uncapped diagnostics tool when
// truncated; nil when empty so that omitempty drops the block entirely on
// healthy scopes.
func diagStrings(diags []engine.Diagnostic) []string {
	if len(diags) == 0 {
		return nil
	}
	shown := diags
	truncated := len(diags) > diagLimit
	if truncated {
		shown = diags[:diagLimit]
	}
	out := make([]string, len(shown), len(shown)+1)
	for i, diag := range shown {
		out[i] = diag.String()
	}
	if truncated {
		out = append(out, fmt.Sprintf("+%d more diagnostics: run the diagnostics tool for the full inventory", len(diags)-diagLimit))
	}
	return out
}

// canonPkg canonicalizes an agent-supplied package address against the
// workspace module: module-prefixed addresses pass through, bare workspace
// directories gain the prefix. File names are refused — packages are
// directories, always spelled alone.
func canonPkg(module engine.PkgPath, addr string) (engine.PkgPath, error) {
	path, ok := engine.CleanPath(addr)
	if !ok {
		return "", fmt.Errorf("invalid package path %q", addr)
	}
	if strings.HasSuffix(path.String(), ".go") {
		return "", fmt.Errorf("%q names a file; package arguments take the package alone", addr)
	}
	if path == "." || engine.PkgPath(path) == module {
		return module, nil
	}
	if strings.HasPrefix(path.String(), module.String()+"/") {
		return engine.PkgPath(path), nil
	}
	return engine.PkgPath(module.String() + "/" + path.String()), nil
}

// fileArg normalizes an agent-supplied file address inside pkg: a bare
// *.go name, or a path accepted when its package agrees — spelled raw
// (dependency and canonical workspace addresses) or workspace-relative.
// Contradictions are refused, never guessed.
func fileArg(module, pkg engine.PkgPath, file string) (string, error) {
	if strings.Contains(file, "/") {
		fpath, ok := engine.CleanPath(file)
		if !ok {
			return "", fmt.Errorf("invalid file path %q", file)
		}
		if engine.PkgPath(fpath.Dir()) != pkg {
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
func pkgAddr(module engine.PkgPath, dir engine.RelativePath) string {
	if dir == "." {
		return module.String()
	}
	return module.String() + "/" + dir.String()
}
