package tools

import (
	"context"
	"fmt"

	"github.com/pedropaccola/gomcp/internal/store"
	"github.com/pedropaccola/gomcp/internal/workspace"
)

// readPackage resolves a package address across both worlds and runs fn
// under a Read call with the resolved identity: workspace first, then the
// dependency cache, lazily loading the dependency on a workspace miss —
// loads never happen under Read. An external dependency address has no
// "relative to the workspace" interpretation, so it's looked up raw,
// never run through NewPackageID's module-prefixing.
func readPackage(ctx context.Context, st *store.Store, addr string, fn func(*store.View, workspace.PackageID) error) error {
	ext := workspace.PackagePath(addr)
	var extOK bool
	attempt := func() (bool, error) {
		found := false
		err := st.Read(ctx, func(v *store.View) error {
			canon, err := workspace.NewPackageID(v.Module(), addr)
			if err != nil {
				return err
			}
			extOK = ext != canon.Base() && ext != "."
			if v.HasPackage(canon) {
				found = true
				return fn(v, canon)
			}
			if extOK {
				if id, ok := v.ExternalPackageID(ext); ok {
					found = true
					return fn(v, id)
				}
			}
			return nil
		})
		return found, err
	}
	if found, err := attempt(); err != nil || found {
		return err
	}
	if !extOK {
		return fmt.Errorf("no package at %q: call list_packages for valid addresses", addr)
	}
	if err := st.LoadExternal(ctx, ext); err != nil {
		return fmt.Errorf("no workspace package at %q, and %v", addr, err)
	}
	if found, err := attempt(); err != nil || found {
		return err
	}
	return workspace.NoPackageError(addr)
}

// readSymbol is readPackage plus symbol resolution: View.Symbol already
// falls through workspace units into the external cache.
func readSymbol(ctx context.Context, st *store.Store, addr, key string, fn func(*store.View, store.Symbol, workspace.PackageID) error) error {
	return readPackage(ctx, st, addr, func(v *store.View, pkg workspace.PackageID) error {
		sym, ok := v.Symbol(pkg.Base(), key)
		if !ok {
			return fmt.Errorf("%s: call list_symbols for valid keys", workspace.NoSymbolError(key, addr))
		}
		return fn(v, sym, sym.Owner)
	})
}

// methodSignatures renders a type's method list the way list_methods and
// describe_symbols present it: one signature line each.
func methodSignatures(v *store.View, pkg workspace.PackageID, typeName string) []string {
	var out []string
	for _, m := range v.Methods(pkg, typeName) {
		if sig, ok := v.Signature(pkg.Base(), m.Key); ok {
			out = append(out, sig)
		}
	}
	return out
}

// readFile is readPackage plus file resolution.
func readFile(ctx context.Context, st *store.Store, addr, fileName string, fn func(*store.View, workspace.FilePath, workspace.PackageID) error) error {
	return readPackage(ctx, st, addr, func(v *store.View, pkg workspace.PackageID) error {
		fp, err := v.ResolveFile(pkg, fileName)
		if err != nil {
			return err
		}
		return fn(v, fp, pkg)
	})
}
