package shapes

// Shape is anything with an area.
type Shape interface {
	Area() float64
}

// Named is a second interface no fixture type implements.
type Named interface {
	Name() string
}

type Circle struct{ R float64 }

func (c Circle) Area() float64 { return 3.14 * c.R * c.R }

// Square implements Shape only through its pointer method set.
type Square struct{ S float64 }

func (s *Square) Area() float64 { return s.S * s.S }

type Base struct{}

func (Base) Area() float64 { return 0 }

// Embedded implements Shape only through promotion — the case syntactic
// matching cannot see.
type Embedded struct{ Base }

type NotShape struct{}
