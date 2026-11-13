package journal

import "time"

// StaleAfter is how long a live run may go without a journal event before
// `pawl status` flags it as idle. It is a policy constant, not a public knob.
const StaleAfter = 7 * 24 * time.Hour

// Stale reports whether rs is a live (non-terminal, so running or blocked)
// run whose last journal event is at least threshold before now. It reads no
// clock: now is the caller's.
func Stale(rs *RunState, now time.Time, threshold time.Duration) bool {
	return false // STUB: tests-first commit
}
