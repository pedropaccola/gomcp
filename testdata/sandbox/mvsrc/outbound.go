package mvsrc

// OutboundExported has no dependents (nothing calls it), but itself calls
// PublicHelper, an exported sibling that stays behind when only
// OutboundExported moves — tests whether the mover's own outbound
// reference to a *remaining, exported* sibling gets requalified.
func OutboundExported() int { return PublicHelper() }

// PublicHelper stays behind — its own export status means moveConflicts
// won't refuse the move (only unexported blocking referrers do), so this
// is a genuinely different case from the existing refusal tests.
func PublicHelper() int { return 99 }
