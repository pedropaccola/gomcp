package tools

import (
	"context"
	"fmt"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/engine"
)

// readPackage resolves a package address across both worlds and runs fn
// under the read gate with the resolved package: workspace first, then the
// dependency cache, lazily loading the dependency on a workspace miss —
// loads never happen under the gate.
func readPackage(ctx context.Context, eng *engine.Engine, addr string, fn func(*engine.View, engine.Package) error) error {
	clean, cleanOK := address.CleanPath(addr)
	ext := address.PkgPath(clean)
	var extOK bool
	attempt := func() (bool, error) {
		found := false
		err := eng.Read(ctx, func(v *engine.View) error {
			canon, err := canonPkg(v.Module(), addr)
			if err != nil {
				return err
			}
			extOK = cleanOK && ext != canon && ext != "."
			if pkg, ok := v.Package(canon); ok {
				found = true
				return fn(v, pkg)
			}
			if extOK {
				if pkg, ok := v.ExternalPackage(ext); ok {
					found = true
					return fn(v, pkg)
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
	if err := eng.LoadExternal(ctx, ext); err != nil {
		return fmt.Errorf("no workspace package at %q, and %v", addr, err)
	}
	if found, err := attempt(); err != nil || found {
		return err
	}
	return fmt.Errorf("no package at %q", addr)
}

// readSymbol is readPackage plus symbol resolution: engine.View.Symbol
// already falls through workspace units into the external cache.
func readSymbol(ctx context.Context, eng *engine.Engine, addr, key string, fn func(*engine.View, engine.Symbol, engine.Package) error) error {
	return readPackage(ctx, eng, addr, func(v *engine.View, pkg engine.Package) error {
		sym, owner, ok := v.Symbol(pkg.PkgPath(), key)
		if !ok {
			return fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, addr)
		}
		return fn(v, sym, owner)
	})
}

// methodSignatures renders a type's method list the way list_methods and
// describe_symbol present it: one signature line each.
func methodSignatures(v *engine.View, pkg engine.Package, typeName string) []string {
	var out []string
	for _, m := range v.Methods(pkg, typeName) {
		if sig, ok := v.Signature(pkg.PkgPath(), m.Key()); ok {
			out = append(out, sig)
		}
	}
	return out
}
