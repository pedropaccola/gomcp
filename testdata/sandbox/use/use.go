package use

import "example.com/sandbox/shapes"

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
