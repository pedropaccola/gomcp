// Package mvdest is an empty destination fixture for MoveSymbol
// cross-package tests — deliberately has no existing relationship to
// mvsrc, so it can receive a moved symbol without any import-cycle risk.
package mvdest

// Existing is here so mvdest isn't a completely empty package.
func Existing() int { return 0 }
