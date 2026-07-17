// Package mvsrc is a fixture for testing MoveSymbol's cross-package
// safety checks and qualifier rewriting.
package mvsrc

// Perimeter returns the circumference of a circle with radius r.
func Perimeter(r float64) float64 { return 2 * 3.14 * r }

// diameterUsingPerimeter is a same-package sibling reference to Perimeter,
// for testing that a cross-package move gains the destination's qualifier.
func diameterUsingPerimeter(r float64) float64 { return Perimeter(r) / 3.14 }

// dependsOnUnexported references qhelper, which stays behind if only this
// function moves — must refuse (Direction 1: dependency conflict).
func dependsOnUnexported() int { return qhelper() }

func qhelper() int { return 42 }

// usesUnexportedThing references unexportedThing, which would otherwise
// leave without it — must refuse (Direction 2b: blocking referrer).
func usesUnexportedThing() int { return unexportedThing() }

func unexportedThing() int { return 1 }

// Box and its method M exist to test method-receiver locality: M cannot
// move alone, only together with Box.
type Box struct{ V int }

func (b Box) M() int { return b.V }
