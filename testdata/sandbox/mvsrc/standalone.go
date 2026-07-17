package mvsrc

// StandaloneFunc has no same-package dependents and no receiver-locality
// entanglement — a clean fixture for testing that MoveFile's cross-package
// move succeeds when moveConflicts finds nothing wrong, while its known
// external-qualifier gap still surfaces as an ordinary diagnostic.
func StandaloneFunc() int { return 7 }
