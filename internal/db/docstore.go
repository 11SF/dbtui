package db

// EffectiveFindLimit returns the limit a Find() call should actually use:
// defaults to 100 when the caller passed 0 (unset), otherwise returns the
// caller's explicit value unchanged — there is no client-side clamp beyond
// the "default if unset" rule (spec §4.1).
func EffectiveFindLimit(limit int) int {
	if limit == 0 {
		return 100
	}
	return limit
}
