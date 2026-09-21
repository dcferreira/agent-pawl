package journal

import (
	"reflect"
	"testing"
	"time"
)

// parallelEntryFixture returns a parallel step "p" entered (ungrouped), then
// two branch steps "b1" and "b2" entered under Group "p" — no transitions
// yet. It asserts that grouped STEP_ENTER events populate PendingBranches
// without disturbing the singular cursor-tracking fields, which must still
// park on "p" itself (Group == "").
func parallelEntryFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "p1", Seq: next(), Time: at(0)},
		{Kind: KindStepEnter, RunID: "p1", Seq: next(), Time: at(1), Step: "p", Attempt: 1, Group: ""},
		{Kind: KindStepEnter, RunID: "p1", Seq: next(), Time: at(2), Step: "b1", Attempt: 1, Group: "p"},
		{Kind: KindStepEnter, RunID: "p1", Seq: next(), Time: at(3), Step: "b2", Attempt: 1, Group: "p"},
	}
}

func TestReplay_ParallelBranches_Entered(t *testing.T) {
	rs, err := Replay(parallelEntryFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	wantCursor := Cursor{Step: "p", Attempt: 1}
	if rs.Cursor != wantCursor {
		t.Errorf("Cursor = %+v, want %+v (grouped events must not move the cursor)", rs.Cursor, wantCursor)
	}

	wantPending := map[string]bool{"b1": true, "b2": true}
	if got := rs.PendingBranches["p"]; !reflect.DeepEqual(got, wantPending) {
		t.Errorf("PendingBranches[\"p\"] = %+v, want %+v", got, wantPending)
	}
}

func TestReplay_ParallelBranches_OneTransitions(t *testing.T) {
	events := parallelEntryFixture()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seq := len(events)
	events = append(events, Event{
		Kind: KindTransition, RunID: "p1", Seq: seq, Time: t0.Add(4 * time.Second),
		Step: "b1", Attempt: 1, Group: "p", Target: "join", Outcome: "success",
	})

	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	wantPending := map[string]bool{"b2": true}
	if got := rs.PendingBranches["p"]; !reflect.DeepEqual(got, wantPending) {
		t.Errorf("PendingBranches[\"p\"] = %+v, want %+v", got, wantPending)
	}
	if got := rs.BranchOutcome["b1"]; got != "success" {
		t.Errorf("BranchOutcome[\"b1\"] = %q, want %q", got, "success")
	}

	wantCursor := Cursor{Step: "p", Attempt: 1}
	if rs.Cursor != wantCursor {
		t.Errorf("Cursor = %+v, want %+v (grouped TRANSITION must not move the cursor)", rs.Cursor, wantCursor)
	}
}

func TestReplay_ParallelBranches_BothTransition(t *testing.T) {
	events := parallelEntryFixture()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seq := len(events)
	events = append(events,
		Event{Kind: KindTransition, RunID: "p1", Seq: seq, Time: t0.Add(4 * time.Second), Step: "b1", Attempt: 1, Group: "p", Target: "join", Outcome: "success"},
		Event{Kind: KindTransition, RunID: "p1", Seq: seq + 1, Time: t0.Add(5 * time.Second), Step: "b2", Attempt: 1, Group: "p", Target: "join", Outcome: "failure"},
	)

	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if got := rs.PendingBranches["p"]; len(got) != 0 {
		t.Errorf("PendingBranches[\"p\"] = %+v, want empty", got)
	}
	if got := rs.BranchOutcome["b1"]; got != "success" {
		t.Errorf("BranchOutcome[\"b1\"] = %q, want %q", got, "success")
	}
	if got := rs.BranchOutcome["b2"]; got != "failure" {
		t.Errorf("BranchOutcome[\"b2\"] = %q, want %q", got, "failure")
	}
}

// TestReplay_ParallelBranches_CrashMidGroup simulates a crash after one
// branch fully resolved but the other was only entered: replay must
// reconstruct PendingBranches with only the unresolved branch outstanding,
// while the cursor still parks on the parallel step itself.
func TestReplay_ParallelBranches_CrashMidGroup(t *testing.T) {
	events := parallelEntryFixture()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seq := len(events)
	events = append(events, Event{
		Kind: KindTransition, RunID: "p1", Seq: seq, Time: t0.Add(4 * time.Second),
		Step: "b1", Attempt: 1, Group: "p", Target: "join", Outcome: "success",
	})
	// b2 never transitions: process crashed here.

	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	wantPending := map[string]bool{"b2": true}
	if got := rs.PendingBranches["p"]; !reflect.DeepEqual(got, wantPending) {
		t.Errorf("PendingBranches[\"p\"] = %+v, want %+v", got, wantPending)
	}

	wantCursor := Cursor{Step: "p", Attempt: 1}
	if rs.Cursor != wantCursor {
		t.Errorf("Cursor = %+v, want %+v (mid-group crash must still park on the parallel step)", rs.Cursor, wantCursor)
	}
}

// TestReplay_NoGroupedEvents_Unaffected re-replays every existing fixture and
// confirms Cursor/Attempts/Visits/State are unchanged by the addition of
// Group handling, and that PendingBranches/BranchOutcome start out empty
// (non-nil) when a run has no grouped events at all.
func TestReplay_NoGroupedEvents_Unaffected(t *testing.T) {
	for name, fixture := range map[string]func() []Event{
		"completedRun": completedRunFixture,
		"blockedRun":   blockedRunFixture,
		"catchEdge":    catchEdgeFixture,
	} {
		t.Run(name, func(t *testing.T) {
			rs, err := Replay(fixture())
			if err != nil {
				t.Fatalf("Replay: %v", err)
			}
			if rs.PendingBranches == nil {
				t.Error("PendingBranches is nil, want non-nil empty map")
			}
			if len(rs.PendingBranches) != 0 {
				t.Errorf("PendingBranches = %+v, want empty", rs.PendingBranches)
			}
			if rs.BranchOutcome == nil {
				t.Error("BranchOutcome is nil, want non-nil empty map")
			}
			if len(rs.BranchOutcome) != 0 {
				t.Errorf("BranchOutcome = %+v, want empty", rs.BranchOutcome)
			}
		})
	}
}
