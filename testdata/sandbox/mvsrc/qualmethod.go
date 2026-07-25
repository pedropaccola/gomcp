package mvsrc

// Gauge and its method Value exist so a cross-package file move exercises
// QualifierFixups against a method call site: Value's own call syntax
// must stay untouched by the move, unlike a bare type/func reference.
type Gauge struct{ N int }

func (g Gauge) Value() int { return g.N }
