package use

import sh "example.com/sandbox/shapes"

// AliasedArea exists to prove aliased imports keep their alias through
// package renames.
func AliasedArea() float64 {
	return sh.Base{}.Area()
}
