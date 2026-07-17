package mvsrc

// AreaOfBox uses the Box type declared in mvsrc.go — a different file.
// Moving just this file (not mvsrc.go) would leave Box behind, an illegal
// method relocation: exercises MoveFile's method-locality check.
func (b Box) AreaOfBox() int { return b.V * b.V }
