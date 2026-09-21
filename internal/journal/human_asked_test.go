package journal

import (
	"testing"
	"time"
)

// TestReplay_HumanAskedIsANoOpEvent: a HUMAN_ASKED event (journalled when a
// `human` step asks its question, DESIGN.md §4) must not trip Replay's
// unknown-Kind error, and must not perturb any other reconstructed field —
// the deadline it anchors is read directly off the journal by
// engine.SubmitHuman, not carried through RunState.
func TestReplay_HumanAskedIsANoOpEvent(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }

	events := []Event{
		{Kind: KindRunStart, RunID: "r1", Seq: 0, Time: at(0)},
		{Kind: KindStepEnter, RunID: "r1", Seq: 1, Time: at(1), Step: "ask", Attempt: 1},
		{Kind: KindHumanAsked, RunID: "r1", Seq: 2, Time: at(2), Step: "ask", Attempt: 1},
	}

	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay with a HUMAN_ASKED event: %v", err)
	}
	if rs.Cursor.Step != "ask" || rs.Cursor.Attempt != 1 {
		t.Fatalf("Cursor = %+v, want {ask 1}: HUMAN_ASKED must not move the cursor", rs.Cursor)
	}
	if rs.Ended {
		t.Fatalf("RunState.Ended = true, want false: HUMAN_ASKED is not a RUN_END")
	}

	// Any prefix must still replay cleanly (Replay's own purity contract).
	if _, err := Replay(events[:2]); err != nil {
		t.Fatalf("Replay(prefix before HUMAN_ASKED): %v", err)
	}
}
