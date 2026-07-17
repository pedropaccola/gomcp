package use

import (
	"example.com/sandbox/mvsrc"
	"example.com/sandbox/shapes"
)

// UsePerimeter is a third-party reference (this package is neither the
// source nor the destination of the move) to Perimeter over in mvsrc, for
// testing that MoveSymbol repoints an already-qualified reference to the
// symbol's new home.
func UsePerimeter(r float64) float64 { return mvsrc.Perimeter(r) }

// UseStandalone references mvsrc.StandaloneFunc, for testing that
// MoveFile's cross-package move doesn't rewrite this external qualifier
// (unlike MoveSymbol) — it's expected to dangle and surface as an
// ordinary diagnostic afterward.
func UseStandalone() int { return mvsrc.StandaloneFunc() }

var c = shapes.Circle{R: 1}

func NewCircle() shapes.Circle {
	return shapes.Circle{R: 2}
}

func UseArea() float64 {
	return c.Area()
}

func TotalArea(ss []shapes.Shape) float64 {
	total := 0.0
	for _, s := range ss {
		total += s.Area()
	}
	return total
}
