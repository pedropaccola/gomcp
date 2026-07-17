package mvsrc

// FileOutbound depends on PublicHelper, declared in outbound.go — a
// different file that isn't moving. Moving just this file tests
// MoveFile's outbound qualifier fixup, since PublicHelper stays behind.
func FileOutbound() int { return PublicHelper() }
