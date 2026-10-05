package journal

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// foreachEvents builds a journal for a foreach parallel step "fe" over three
// items, with seq/time assigned automatically; each argument is one event.
func foreachEvents(evs ...Event) []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := []Event{{Kind: KindRunStart, RunID: "f1", Seq: 0, Time: t0}}
	for i, e := range evs {
		e.RunID = "f1"
		e.Seq = i + 1
		e.Time = t0.Add(time.Duration(i+1) * time.Second)
		out = append(out, e)
	}
	return out
}

func intp(i int) *int { return &i }

func foreachEnter(items []any) Event {
	return Event{Kind: KindStepEnter, Step: "fe", Attempt: 1, Items: items}
}

func itemEnter(i int) Event {
	return Event{Kind: KindStepEnter, Step: "body", Attempt: 1, Group: "fe", Item: intp(i)}
}

func itemTransition(i int, outcome string) Event {
	return Event{Kind: KindTransition, Step: "body", Attempt: 1, Group: "fe", Item: intp(i), Target: "join", Outcome: outcome}
}

func TestReplay_Foreach_MidFlight(t *testing.T) {
	items := []any{"a", "b", "c"}
	rs, err := Replay(foreachEvents(
		foreachEnter(items),
		itemEnter(0), itemEnter(1), itemEnter(2),
		Event{Kind: KindWrites, Step: "body", Group: "fe", Item: intp(0), Writes: map[string]any{"r": "x"}},
		itemTransition(0, "success"),
		itemTransition(2, "failure"),
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got := rs.ForeachItems["fe"]; !reflect.DeepEqual(got, items) {
		t.Errorf("ForeachItems[fe] = %v, want %v", got, items)
	}
	if want := map[int]bool{1: true}; !reflect.DeepEqual(rs.PendingItems["fe"], want) {
		t.Errorf("PendingItems[fe] = %v, want %v", rs.PendingItems["fe"], want)
	}
	if want := map[int]string{0: "success", 2: "failure"}; !reflect.DeepEqual(rs.ItemOutcome["fe"], want) {
		t.Errorf("ItemOutcome[fe] = %v, want %v", rs.ItemOutcome["fe"], want)
	}
	if want := map[string]any{"r": "x"}; !reflect.DeepEqual(rs.ItemWrites["fe"][0], want) {
		t.Errorf("ItemWrites[fe][0] = %v, want %v", rs.ItemWrites["fe"][0], want)
	}
	if want := (Cursor{Step: "fe", Attempt: 1}); rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v (item events must not move the cursor)", rs.Cursor, want)
	}
	if len(rs.PendingBranches) != 0 || len(rs.BranchOutcome) != 0 {
		t.Errorf("item events leaked into branch maps: %v %v", rs.PendingBranches, rs.BranchOutcome)
	}
	if len(rs.State) != 0 {
		t.Errorf("State = %v, want item writes isolated from it", rs.State)
	}
}

func TestReplay_Foreach_Complete(t *testing.T) {
	rs, err := Replay(foreachEvents(
		foreachEnter([]any{"a", "b"}),
		itemEnter(0), itemEnter(1),
		itemTransition(1, "success"), itemTransition(0, "success"),
		Event{Kind: KindTransition, Step: "fe", Attempt: 1, Target: "next", Outcome: "success"},
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(rs.PendingItems["fe"]) != 0 {
		t.Errorf("PendingItems[fe] = %v, want empty", rs.PendingItems["fe"])
	}
	if want := map[int]string{0: "success", 1: "success"}; !reflect.DeepEqual(rs.ItemOutcome["fe"], want) {
		t.Errorf("ItemOutcome[fe] = %v, want %v", rs.ItemOutcome["fe"], want)
	}
	if want := (Cursor{Step: "next", Attempt: 1}); rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v", rs.Cursor, want)
	}
}

func TestReplay_Foreach_ReentryResets(t *testing.T) {
	rs, err := Replay(foreachEvents(
		foreachEnter([]any{"a", "b"}),
		itemEnter(0), itemEnter(1),
		Event{Kind: KindWrites, Step: "body", Group: "fe", Item: intp(0), Writes: map[string]any{"r": "x"}},
		itemTransition(0, "success"), itemTransition(1, "failure"),
		Event{Kind: KindTransition, Step: "fe", Attempt: 1, Target: "fe", Outcome: "failure"},
		foreachEnter([]any{"z"}),
		itemEnter(0),
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if want := []any{"z"}; !reflect.DeepEqual(rs.ForeachItems["fe"], want) {
		t.Errorf("ForeachItems[fe] = %v, want %v", rs.ForeachItems["fe"], want)
	}
	if want := map[int]bool{0: true}; !reflect.DeepEqual(rs.PendingItems["fe"], want) {
		t.Errorf("PendingItems[fe] = %v, want %v", rs.PendingItems["fe"], want)
	}
	if len(rs.ItemOutcome["fe"]) != 0 || len(rs.ItemWrites["fe"]) != 0 {
		t.Errorf("per-item maps not reset: outcome=%v writes=%v", rs.ItemOutcome["fe"], rs.ItemWrites["fe"])
	}
}

func TestEvent_ItemJSONRoundTrip(t *testing.T) {
	data, err := json.Marshal(Event{Kind: KindStepEnter, Group: "fe", Item: intp(0)})
	if err != nil {
		t.Fatal(err)
	}
	var back Event
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Item == nil || *back.Item != 0 {
		t.Errorf("Item = %v, want pointer to 0 (index 0 must survive omitempty)", back.Item)
	}
	data, _ = json.Marshal(Event{Kind: KindStepEnter})
	if string(data) != `{"kind":"STEP_ENTER","run_id":"","seq":0,"time":"0001-01-01T00:00:00Z"}` {
		t.Errorf("event without item/items changed shape: %s", data)
	}
}

func TestReplay_Foreach_OldJournalUnchanged(t *testing.T) {
	rs, err := Replay(parallelEntryFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(rs.ForeachItems) != 0 || len(rs.PendingItems) != 0 || len(rs.ItemOutcome) != 0 || len(rs.ItemWrites) != 0 {
		t.Errorf("foreach maps populated by a branch-only journal: %+v", rs)
	}
}

// TestReplay_Foreach_ItemReenterResets: a fresh grouped STEP_ENTER for an
// item (a crash-resume re-running it) discards that item's earlier writes
// and outcome; a hard-retry STEP_ENTER (HardRetry > 0) of the same try does
// not; and other items are untouched.
func TestReplay_Foreach_ItemReenterResets(t *testing.T) {
	retryEnter := itemEnter(0)
	retryEnter.HardRetry = 1
	rs, err := Replay(foreachEvents(
		foreachEnter([]any{"a", "b"}),
		itemEnter(0), itemEnter(1),
		Event{Kind: KindWrites, Step: "body", Group: "fe", Item: intp(0), Writes: map[string]any{"a": "old"}},
		Event{Kind: KindWrites, Step: "body", Group: "fe", Item: intp(1), Writes: map[string]any{"a": "keep"}},
		itemTransition(1, "success"),
		retryEnter, // same try: must not reset
		Event{Kind: KindWrites, Step: "body", Group: "fe", Item: intp(0), Writes: map[string]any{"c": "retry"}},
		itemTransition(0, "failure"),
		itemEnter(0), // fresh re-run: resets item 0 only
		Event{Kind: KindWrites, Step: "body", Group: "fe", Item: intp(0), Writes: map[string]any{"b": "new"}},
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if want := map[string]any{"b": "new"}; !reflect.DeepEqual(rs.ItemWrites["fe"][0], want) {
		t.Errorf("ItemWrites[fe][0] = %v, want %v (stale writes must be gone)", rs.ItemWrites["fe"][0], want)
	}
	if want := map[string]any{"a": "keep"}; !reflect.DeepEqual(rs.ItemWrites["fe"][1], want) {
		t.Errorf("ItemWrites[fe][1] = %v, want %v", rs.ItemWrites["fe"][1], want)
	}
	if _, ok := rs.ItemOutcome["fe"][0]; ok {
		t.Errorf("ItemOutcome[fe][0] = %v, want cleared by the re-entry", rs.ItemOutcome["fe"][0])
	}
	if rs.ItemOutcome["fe"][1] != "success" || !rs.PendingItems["fe"][0] {
		t.Errorf("outcome=%v pending=%v", rs.ItemOutcome["fe"], rs.PendingItems["fe"])
	}
}

// TestReplay_Foreach_HardRetryKeepsWrites: the HardRetry > 0 STEP_ENTER of
// one try never resets that item's writes.
func TestReplay_Foreach_HardRetryKeepsWrites(t *testing.T) {
	retryEnter := itemEnter(0)
	retryEnter.HardRetry = 1
	rs, err := Replay(foreachEvents(
		foreachEnter([]any{"a"}),
		itemEnter(0),
		Event{Kind: KindWrites, Step: "body", Group: "fe", Item: intp(0), Writes: map[string]any{"k": "v"}},
		retryEnter,
	))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"k": "v"}; !reflect.DeepEqual(rs.ItemWrites["fe"][0], want) {
		t.Errorf("ItemWrites = %v, want %v", rs.ItemWrites["fe"][0], want)
	}
}

func itemRetryEnter(i, attempt int, key string) Event {
	return Event{Kind: KindStepEnter, Step: "body", Attempt: attempt, AttemptKey: key, Retry: true, Group: "fe", Item: intp(i)}
}

func TestReplay_Foreach_ItemAttemptTracking(t *testing.T) {
	rs, err := Replay(foreachEvents(
		foreachEnter([]any{"a", "b"}),
		itemEnter(0), itemEnter(1),
		Event{Kind: KindWrites, Step: "body", Group: "fe", Item: intp(0), Writes: map[string]any{"r": "x"}},
		Event{Kind: KindPostcondition, Step: "body", Attempt: 1, Group: "fe", Item: intp(0), Text: "boom", AttemptKey: "k1"},
		itemRetryEnter(0, 2, "k1"),
		Event{Kind: KindPostcondition, Step: "body", Attempt: 2, Group: "fe", Item: intp(0), Text: "bang", AttemptKey: "k2"},
		itemRetryEnter(0, 3, "k2"),
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got := rs.ItemAttempt["fe"]; !reflect.DeepEqual(got, map[int]int{0: 3, 1: 1}) {
		t.Errorf("ItemAttempt = %v", got)
	}
	if got := rs.ItemAttemptKey["fe"]; !reflect.DeepEqual(got, map[int]string{0: "k2", 1: ""}) {
		t.Errorf("ItemAttemptKey = %v", got)
	}
	if got := rs.ItemKeyAttempts["fe"][0]; !reflect.DeepEqual(got, map[string]int{"": 1, "k1": 2, "k2": 3}) {
		t.Errorf("ItemKeyAttempts[0] = %v", got)
	}
	// a Retry entry keeps the item's writes and visit counts untouched
	if want := map[string]any{"r": "x"}; !reflect.DeepEqual(rs.ItemWrites["fe"][0], want) {
		t.Errorf("ItemWrites = %v, want kept across a retry", rs.ItemWrites["fe"][0])
	}
	if rs.Visits["body"] != 0 || rs.LastError != "" {
		t.Errorf("Visits[body]=%d LastError=%q, want untouched", rs.Visits["body"], rs.LastError)
	}
	if !rs.PendingItems["fe"][0] {
		t.Error("item 0 should still be pending")
	}
}

func TestReplay_Foreach_ItemAttemptResetByFreshEntry(t *testing.T) {
	rs, err := Replay(foreachEvents(
		foreachEnter([]any{"a"}),
		itemEnter(0),
		itemRetryEnter(0, 2, "k1"),
		itemEnter(0), // a fresh (crash-resume) entry starts the item over
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if rs.ItemAttempt["fe"][0] != 1 || rs.ItemAttemptKey["fe"][0] != "" {
		t.Errorf("ItemAttempt=%v key=%v, want reset to 1/\"\"", rs.ItemAttempt["fe"], rs.ItemAttemptKey["fe"])
	}
	if got := rs.ItemKeyAttempts["fe"][0]; !reflect.DeepEqual(got, map[string]int{"": 1}) {
		t.Errorf("ItemKeyAttempts[0] = %v, want only the fresh entry", got)
	}
}

func TestReplay_Foreach_ItemAttemptResetByForeachReentry(t *testing.T) {
	rs, err := Replay(foreachEvents(
		foreachEnter([]any{"a"}),
		itemEnter(0), itemRetryEnter(0, 2, "k1"), itemTransition(0, "failure"),
		Event{Kind: KindTransition, Step: "fe", Attempt: 1, Target: "fe", Outcome: "failure"},
		foreachEnter([]any{"a"}),
	))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(rs.ItemAttempt["fe"]) != 0 || len(rs.ItemAttemptKey["fe"]) != 0 || len(rs.ItemKeyAttempts["fe"]) != 0 {
		t.Errorf("not reset: %v %v %v", rs.ItemAttempt, rs.ItemAttemptKey, rs.ItemKeyAttempts)
	}
}
