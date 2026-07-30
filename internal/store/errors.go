package store

import "fmt"

// errNoFile reports that name names no file in pkg.
func errNoFile(name string, pkg any) error {
	return fmt.Errorf("no file %q in %q", name, pkg)
}

// errFileExists reports that path already names a file in the workspace.
func errFileExists(path any) error {
	return fmt.Errorf("file %q already exists", path)
}

// errSymbolExists reports that key already names a symbol in pkg.
func errSymbolExists(key string, pkg any) error {
	return fmt.Errorf("symbol %q already exists in %q", key, pkg)
}
