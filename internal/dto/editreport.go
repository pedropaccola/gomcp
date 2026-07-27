package dto

import "github.com/pedropaccola/gomcp/internal/address"

// EditReport is the echo of a committed transaction.
type EditReport struct {
	Changed   []address.FilePath // files created, modified, moved, or deleted by this Tx
	Delta     []Diagnostic       // diagnostics introduced by this transaction
	Resolved  []Diagnostic       // pre-existing diagnostics this transaction fixed
	Unrelated int                // pre-existing diagnostics this transaction left untouched
	Stale     bool               // recheck failed: state applied, Delta unavailable
	Note      string             // human-readable recheck failure, when Stale
}
