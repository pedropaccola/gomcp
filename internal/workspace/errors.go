package workspace

import "fmt"

// NoPackageError reports that id names no package in the workspace.
func NoPackageError(id any) error {
	return fmt.Errorf("no package at %q", id)
}

// NoSymbolError reports that key names no symbol in pkg.
func NoSymbolError(key string, pkg any) error {
	return fmt.Errorf("no symbol %q in %q", key, pkg)
}

// NoInsertionPointError reports that path has no computable insertion
// point for a new declaration — should not happen for a well-formed file.
func NoInsertionPointError(path any) error {
	return fmt.Errorf("cannot locate insertion point in %q", path)
}

// PackageExistsError reports that pkg already names a package in the
// workspace.
func PackageExistsError(pkg any) error {
	return fmt.Errorf("a package already exists at %q", pkg)
}

// InvalidPackageNameError reports that name isn't a legal Go identifier.
func InvalidPackageNameError(name string) error {
	return fmt.Errorf("%q is not a valid package name", name)
}

// OutsideModuleCreateError reports that pkg can't be created because it
// names the module root itself, not a package under it.
func OutsideModuleCreateError(pkg, module any) error {
	return fmt.Errorf("cannot create a package at %q: workspace packages live under module %q", pkg, module)
}

// VanishedError reports an internal invariant violation: subject
// resolved successfully earlier in the same call but no longer resolves
// when the second lookup ran.
func VanishedError(subject any, when string) error {
	return fmt.Errorf("internal error: %q vanished %s", subject, when)
}

// errNoTypeInfo reports that sym's go/types object is unavailable — the
// gate every reference-resolution scanner needs before consulting Uses.
func errNoTypeInfo(key string) error {
	return fmt.Errorf("type information unavailable for %q", key)
}

// errNotInSource reports that key's byte span could not be located in
// its own file — an internal invariant failure, not a user error.
func errNotInSource(key string) error {
	return fmt.Errorf("cannot locate %q in source", key)
}
