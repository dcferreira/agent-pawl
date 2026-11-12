package journal

import "testing"

// testdata/compat/from is a frozen journal of a run moved with
// `pawl run --from`: records 0-6 have the shape the v1.0.0 binary wrote for
// a run blocked at gate (an unrouted failure, journaled as a via_catch
// TRANSITION to blocked and RUN_END{blocked}); records 7-8 are the move,
// RESUME{intervention} and TRANSITION{via_from} back to prep, cut before
// prep is entered (as a crash would leave it), with values redacted to
// placeholders. It uses only v1.0.0 event kinds; via_from is the one key
// v1.0.0 does not know, and Replay does not read it. Every later build must
// replay it to the same cursor and counters that v1.0.0's Replay does, so an
// older 1.x binary and this one resume a moved run at the same step.
const compatFromDir = "testdata/compat/from"

func TestCompatFrom_EventsReplay(t *testing.T) {
	events, err := ReadEvents(compatFromDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 9 {
		t.Fatalf("events = %d, want 9", len(events))
	}
	if last := events[8]; last.Kind != KindTransition || !last.ViaFrom || last.Target != "prep" {
		t.Fatalf("last event = %+v, want TRANSITION{via_from, target prep}", last)
	}
	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if rs.Cursor != (Cursor{Step: "prep", Attempt: 1}) {
		t.Errorf("cursor = %+v, want prep attempt 1", rs.Cursor)
	}
	if rs.Status() != "running" || rs.Terminal() {
		t.Errorf("status = %s, want running (the move un-ends the blocked run)", rs.Status())
	}
	if rs.Visits["prep"] != 1 || rs.Visits["gate"] != 1 {
		t.Errorf("visits = %v, want prep 1, gate 1 (the target is not entered yet)", rs.Visits)
	}
	if len(rs.Attempts) != 0 {
		t.Errorf("attempts = %v, want none (the move clears gate's budget)", rs.Attempts)
	}
	if rs.ResumedInFlight != "" || rs.PendingIntervention {
		t.Errorf("ResumedInFlight = %q, PendingIntervention = %v; want neither (the move's TRANSITION clears both)", rs.ResumedInFlight, rs.PendingIntervention)
	}
}
