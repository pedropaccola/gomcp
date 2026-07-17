package mvbeta

import "example.com/sandbox/mvalpha"

// AlreadyHere calls Solo before it moves in — once Solo moves to mvbeta,
// this reference should lose its qualifier.
func AlreadyHere() int { return mvalpha.Solo() }
