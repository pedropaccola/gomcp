package shapes_test

import (
	"testing"

	"example.com/sandbox/shapes"
)

func TestAreaExternal(t *testing.T) {
	var s shapes.Shape = shapes.Circle{R: 2}
	if s.Area() <= 0 {
		t.Fatal("area must be positive")
	}
}
