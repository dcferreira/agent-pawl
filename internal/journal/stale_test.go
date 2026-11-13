package journal

import (
	"testing"
	"time"
)

func TestReplay_LastEventTime(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }

	events := []Event{
		{Kind: KindRunStart, RunID: "r1", Seq: 0, Time: at(0)},
		{Kind: KindStepEnter, RunID: "r1", Seq: 1, Time: at(1), Step: "ask", Attempt: 1},
		{Kind: KindHumanAsked, RunID: "r1", Seq: 2, Time: at(5), Step: "ask", Attempt: 1},
	}
	rs, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if !rs.LastEventTime.Equal(at(5)) {
		t.Errorf("LastEventTime = %v, want %v (the last event's Time)", rs.LastEventTime, at(5))
	}
	// Any prefix reports its own last event: Replay stays a pure function of its argument.
	rs, err = Replay(events[:2])
	if err != nil {
		t.Fatal(err)
	}
	if !rs.LastEventTime.Equal(at(1)) {
		t.Errorf("prefix LastEventTime = %v, want %v", rs.LastEventTime, at(1))
	}
	rs, err = Replay(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rs.LastEventTime.IsZero() {
		t.Errorf("empty journal LastEventTime = %v, want zero", rs.LastEventTime)
	}
}

func TestStale(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	running := []Event{
		{Kind: KindRunStart, RunID: "r1", Seq: 0, Time: t0},
		{Kind: KindStepEnter, RunID: "r1", Seq: 1, Time: t0, Step: "s", Attempt: 1},
	}
	blocked := append(append([]Event{}, running...),
		Event{Kind: KindRunEnd, RunID: "r1", Seq: 2, Time: t0, Status: "blocked", Reason: "x", Step: "s"})
	abandoned := append(append([]Event{}, running...),
		Event{Kind: KindRunEnd, RunID: "r1", Seq: 2, Time: t0, Status: "abandoned", Reason: "x"})

	mk := func(events []Event) *RunState {
		rs, err := Replay(events)
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	thr := StaleAfter
	for _, tc := range []struct {
		name string
		rs   *RunState
		now  time.Time
		want bool
	}{
		{"running, just under the threshold", mk(running), t0.Add(thr - time.Second), false},
		{"running, exactly at the threshold", mk(running), t0.Add(thr), true},
		{"running, well past", mk(running), t0.Add(22 * 24 * time.Hour), true},
		{"blocked, past", mk(blocked), t0.Add(thr + time.Hour), true},
		{"blocked, under", mk(blocked), t0.Add(time.Hour), false},
		{"abandoned is terminal, never stale", mk(abandoned), t0.Add(100 * 24 * time.Hour), false},
		{"clock before the last event", mk(running), t0.Add(-time.Hour), false},
		{"no events: unknown, never stale", mk(nil), t0.Add(100 * 24 * time.Hour), false},
		{"nil state (opaque run)", nil, t0.Add(100 * 24 * time.Hour), false},
	} {
		if got := Stale(tc.rs, tc.now, thr); got != tc.want {
			t.Errorf("%s: Stale = %v, want %v", tc.name, got, tc.want)
		}
	}
}
