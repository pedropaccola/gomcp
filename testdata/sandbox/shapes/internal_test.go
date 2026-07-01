package shapes

import "testing"

func TestAreaInternal(t *testing.T) {
	if (Circle{R: 1}).Area() <= 0 {
		t.Fatal("area must be positive")
	}
}
