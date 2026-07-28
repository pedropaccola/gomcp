// Package testutil holds fixture builders shared by more than one
// package's tests: workspace and store both need a go/types-backed
// Workspace built from in-memory source with no real module on disk;
// store and tools both need the testdata/sandbox module bootstrapped
// through a real Store. Each package still calls through its own
// locally-named test helper (simpleFixture, viewFixture, sandboxStore,
// ...) — what moved here is the construction logic, not the calling
// convention, so this package has no bearing on how a test reads.
package testutil
