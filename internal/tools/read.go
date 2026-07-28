package tools

import (
	"context"
	"fmt"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/store"
)

// readPackage resolves a package address across both worlds and runs fn
// under a Read call with the resolved package: workspace first, then the
// dependency cache, lazily loading the dependency on a workspace miss —
// loads never happen under Read. An external dependency address has no
// "relative to the workspace" interpretation, so it's looked up raw,
// never run through NewPkgPath's module-prefixing.
func readPackage(ctx context.Context, eng *store.Store, addr string, fn func(*store.View, dto.Package) error) error {
	ext := address.PkgPath(addr)
	var extOK bool
	attempt := func() (bool, error) {
		found := false
		err := eng.Read(ctx, func(v *store.View) error {
			canon, err := address.NewPkgPath(v.Module(), addr)
			if err != nil {
				return err
			}
			extOK = ext != canon && ext != "."
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

// readSymbol is readPackage plus symbol resolution: View.Symbol already
// falls through workspace units into the external cache.
func readSymbol(ctx context.Context, eng *store.Store, addr, key string, fn func(*store.View, dto.Symbol, dto.Package) error) error {
	return readPackage(ctx, eng, addr, func(v *store.View, pkg dto.Package) error {
		sym, owner, ok := v.Symbol(pkg.Path, key)
		if !ok {
			return fmt.Errorf("no symbol %q in package %q: call list_symbols for valid keys", key, addr)
		}
		return fn(v, sym, owner)
	})
}

// methodSignatures renders a type's method list the way list_methods and
// describe_symbol present it: one signature line each.
func methodSignatures(v *store.View, pkg dto.Package, typeName string) []string {
	var out []string
	for _, m := range v.Methods(pkg, typeName) {
		if sig, ok := v.Signature(pkg.Path, m.Key); ok {
			out = append(out, sig)
		}
	}
	return out
}

// readFile is readPackage plus file resolution: View.File resolves a bare
// filename against the already-resolved package.
func readFile(ctx context.Context, eng *store.Store, addr, fileName string, fn func(*store.View, dto.File, dto.Package) error) error {
	return readPackage(ctx, eng, addr, func(v *store.View, pkg dto.Package) error {
		file, err := v.File(pkg, fileName)
		if err != nil {
			return err
		}
		return fn(v, file, pkg)
	})
}
