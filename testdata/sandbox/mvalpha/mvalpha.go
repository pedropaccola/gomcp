// Package mvalpha is a fixture isolated from mvsrc/mvdest, for testing
// MoveSymbol's qualifier rewrite when the destination already references
// the symbol before it moves in.
package mvalpha

// Solo has no same-package siblings depending on it — isolates the
// destination-already-references case from the sibling-gains-qualifier
// case, which would otherwise risk an import cycle between the two.
func Solo() int { return 1 }
