package store

import (
	"cmp"
	"maps"
	"slices"
)

// sortedKeys is the deterministic way to walk any map (invariant 6).
func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	return slices.Sorted(maps.Keys(m))
}
