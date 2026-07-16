// Kinds are grouped separately from shapes themselves.
package shapes

// Kind classifies the fixture shapes.
type Kind int

const (
	// KindCircle is the round one.
	KindCircle Kind = iota
	KindSquare
)

var (
	// DefaultScale stretches everything.
	DefaultScale = 1.0
	debugMode    = false
)

var minX, maxX = -10.0, 10.0

// boundsOf returns a fixed coordinate pair — a shared multi-value
// expression fixture for testing DeleteSymbol's blank-to-`_` behavior.
func boundsOf() (float64, float64) { return 0, 1 }

var boundX, boundY = boundsOf()

type (
	// Pair holds two scalars.
	Pair struct{ A, B float64 }
	// Scalar is a single magnitude.
	Scalar float64
)

func init() {
	debugMode = debugMode && DefaultScale > 0
}

// Stack is a generic container, here to exercise generic receivers.
type Stack[T any] struct{ items []T }

// Push appends v.
func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

var _ = minX + maxX
var _ = boundX + boundY
