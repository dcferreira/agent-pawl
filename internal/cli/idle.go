package cli

import (
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// formatIdleAge renders an idle duration as whole days at 1d or more,
// otherwise whole hours, rounded down.
func formatIdleAge(d time.Duration) string {
	return "" // STUB: tests-first commit
}

// idleAge is the idle label for a replayed run: "" unless the run is stale
// (journal.Stale at journal.StaleAfter), otherwise formatIdleAge of the time
// since its last journal event.
func idleAge(rs *journal.RunState, now time.Time) string {
	return "" // STUB: tests-first commit
}
