package cli

import (
	"strconv"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// formatIdleAge renders an idle duration as whole days at 1d or more,
// otherwise whole hours, rounded down.
func formatIdleAge(d time.Duration) string {
	const oneDay = 24 * time.Hour
	if d >= oneDay {
		return strconv.FormatInt(int64(d/oneDay), 10) + "d"
	}
	return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
}

// idleAge is the idle label for a replayed run: "" unless the run is stale
// (journal.Stale at journal.StaleAfter), otherwise formatIdleAge of the time
// since its last journal event. The clock is the caller's (nowFunc).
func idleAge(rs *journal.RunState, now time.Time) string {
	if !journal.Stale(rs, now, journal.StaleAfter) {
		return ""
	}
	return formatIdleAge(now.Sub(rs.LastEventTime))
}

// idleLine is the `idle:` value in pawl status for a stale run.
func idleLine(age, runID string) string {
	return age + " since the last journal event; to drop it: pawl abandon --run " + runID
}

// lastEventTimeJSON is status --json's last_event_time: the last journal
// event's Time as RFC3339 UTC, or "" when it cannot be known.
func lastEventTimeJSON(rs *journal.RunState) string {
	if rs == nil || rs.LastEventTime.IsZero() {
		return ""
	}
	return rs.LastEventTime.UTC().Format(time.RFC3339)
}
