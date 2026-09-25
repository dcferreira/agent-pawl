package journal

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// completedRunFixture returns the events of one full, successful run: two
// steps, the second retried once after a failing postcondition under attempt
// key "h1" (a second, different key would start a fresh budget — untested
// here since this fixture never hits one), ending done. Both transitions are
// non-catch edges, so each step's attempt budget is cleared the moment it
// leaves. The retry's own STEP_ENTER carries Retry: true (C1): it re-runs
// "test" within the same visit, so it must not count as a second visit.
func completedRunFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "r1", Seq: next(), Time: at(0), Args: map[string]any{"branch": "feat/x"}},
		{Kind: KindStepEnter, RunID: "r1", Seq: next(), Time: at(1), Step: "build", Attempt: 1},
		{Kind: KindWrites, RunID: "r1", Seq: next(), Time: at(2), Step: "build", Attempt: 1, Writes: map[string]any{"artifact": "a.bin"}},
		{Kind: KindPostcondition, RunID: "r1", Seq: next(), Time: at(3), Step: "build", Attempt: 1, OK: true},
		{Kind: KindTransition, RunID: "r1", Seq: next(), Time: at(4), Step: "build", Attempt: 1, Target: "test", Outcome: "success", ViaCatch: false},
		{Kind: KindStepEnter, RunID: "r1", Seq: next(), Time: at(5), Step: "test", Attempt: 1},
		{Kind: KindPostcondition, RunID: "r1", Seq: next(), Time: at(6), Step: "test", Attempt: 1, OK: false, Text: "1 failure", AttemptKey: "h1"},
		{Kind: KindStepEnter, RunID: "r1", Seq: next(), Time: at(7), Step: "test", Attempt: 2, AttemptKey: "h1", Retry: true},
		{Kind: KindWrites, RunID: "r1", Seq: next(), Time: at(8), Step: "test", Attempt: 2, Writes: map[string]any{"tests_passed": true}},
		{Kind: KindPostcondition, RunID: "r1", Seq: next(), Time: at(9), Step: "test", Attempt: 2, OK: true},
		{Kind: KindTransition, RunID: "r1", Seq: next(), Time: at(10), Step: "test", Attempt: 2, Target: "done", Outcome: "success", ViaCatch: false},
		{Kind: KindRunEnd, RunID: "r1", Seq: next(), Time: at(11), Status: "ok"},
	}
}

// blockedRunFixture returns a run that hits an invariant violation partway
// through and blocks. Per DESIGN.md §5, an invariant is an engine check
// evaluated after step completion, not a routed outcome: it ends the run
// directly with RUN_END{blocked}, with no intervening TRANSITION — the step
// never "leaves" in the sense the §B.4 clearing rule means, so its attempt
// budget must still be there for blocked-resume to report. RUN_END carries
// the offending step, per the engine's contract (Replay uses this as the
// primary source of the blocked step — a custom terminal id declared
// status: blocked, per Ruling R2, would instead reach blocked via a normal
// TRANSITION, which RUN_END.Step still names).
func blockedRunFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "r2", Seq: next(), Time: at(0)},
		{Kind: KindStepEnter, RunID: "r2", Seq: next(), Time: at(1), Step: "deploy", Attempt: 1},
		{Kind: KindWrites, RunID: "r2", Seq: next(), Time: at(2), Step: "deploy", Attempt: 1, Writes: map[string]any{"url": "https://x"}},
		{Kind: KindPostcondition, RunID: "r2", Seq: next(), Time: at(3), Step: "deploy", Attempt: 1, OK: true},
		{Kind: KindRunEnd, RunID: "r2", Seq: next(), Time: at(4), Status: "blocked", Step: "deploy", Reason: "vcs-mutated-outside-guard"},
	}
}

// catchEdgeFixture is round-2 finding N2's regression fixture: "test" fails
// its one postcondition, exhausts its attempt budget, and leaves via a
// catch edge (ViaCatch: true) to "recover". §B.4's clearing rule must NOT
// fire here — a catch edge preserves the countdown — so (test,"hX") must
// still be present in Attempts after the transition. Neither prior fixture
// contains a ViaCatch: true transition at all, which is exactly the gap N2
// found: the reviewer's `if !e.ViaCatch` -> `if true` mutation passed
// unnoticed because nothing here exercised the "true" branch's difference
// from "false".
func catchEdgeFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "r3", Seq: next(), Time: at(0)},
		{Kind: KindStepEnter, RunID: "r3", Seq: next(), Time: at(1), Step: "test", Attempt: 1},
		{Kind: KindPostcondition, RunID: "r3", Seq: next(), Time: at(2), Step: "test", Attempt: 1, OK: false, Text: "boom", AttemptKey: "hX"},
		{Kind: KindTransition, RunID: "r3", Seq: next(), Time: at(3), Step: "test", Attempt: 1, Target: "recover", Outcome: "exhausted", ViaCatch: true},
		{Kind: KindStepEnter, RunID: "r3", Seq: next(), Time: at(4), Step: "recover", Attempt: 1},
		{Kind: KindRunEnd, RunID: "r3", Seq: next(), Time: at(5), Status: "ok"},
	}
}

// blockedAtHigherAttemptFixture is round-2 finding N3's regression fixture:
// the blocked step's cursor attempt must reflect a real, non-1 attempt
// number. blockedRunFixture blocks at attempt 1, which cannot distinguish a
// correct implementation from one that hard-codes 1 — exactly the gap the
// reviewer's mutation found.
func blockedAtHigherAttemptFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "r4", Seq: next(), Time: at(0)},
		{Kind: KindStepEnter, RunID: "r4", Seq: next(), Time: at(1), Step: "deploy", Attempt: 1},
		{Kind: KindPostcondition, RunID: "r4", Seq: next(), Time: at(2), Step: "deploy", Attempt: 1, OK: false, Text: "first failure", AttemptKey: "hA"},
		{Kind: KindStepEnter, RunID: "r4", Seq: next(), Time: at(3), Step: "deploy", Attempt: 2, AttemptKey: "hA", Retry: true},
		{Kind: KindWrites, RunID: "r4", Seq: next(), Time: at(4), Step: "deploy", Attempt: 2, Writes: map[string]any{"url": "https://x"}},
		{Kind: KindPostcondition, RunID: "r4", Seq: next(), Time: at(5), Step: "deploy", Attempt: 2, OK: true},
		{Kind: KindRunEnd, RunID: "r4", Seq: next(), Time: at(6), Status: "blocked", Step: "deploy", Reason: "vcs-mutated-outside-guard"},
	}
}

// catchEdgeAfterPassFixture is round-3 finding G1's regression fixture:
// catchEdgeFixture's catch edge is always preceded by a failing
// postcondition, so its two conjuncts (!ViaCatch, postcondition-passed) move
// together and deleting `!e.ViaCatch` from the clearing condition entirely
// (leaving only the postcondition check) passes the whole suite unnoticed.
// A catch edge taken after a PASSING postcondition is reachable for real —
// e.g. max_visits/max_steps exhaustion routes via catch regardless of
// whether this particular attempt's own postcondition passed — and must
// still preserve the budget.
func catchEdgeAfterPassFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "r5", Seq: next(), Time: at(0)},
		{Kind: KindStepEnter, RunID: "r5", Seq: next(), Time: at(1), Step: "test", Attempt: 1},
		{Kind: KindPostcondition, RunID: "r5", Seq: next(), Time: at(2), Step: "test", Attempt: 1, OK: true},
		{Kind: KindTransition, RunID: "r5", Seq: next(), Time: at(3), Step: "test", Attempt: 1, Target: "handle_exhaustion", Outcome: "exhausted", ViaCatch: true},
		{Kind: KindStepEnter, RunID: "r5", Seq: next(), Time: at(4), Step: "handle_exhaustion", Attempt: 1},
		{Kind: KindRunEnd, RunID: "r5", Seq: next(), Time: at(5), Status: "ok"},
	}
}

// noPostconditionFixture is round-3 finding G2's regression fixture: a
// deterministic step with no postcondition: at all (legitimate per §B.7)
// leaves by a normal, non-catch edge. Nothing here ever emits a
// POSTCONDITION event for "build", so this exercises the STEP_ENTER-time
// `= true` default entirely on its own — flipping that default to `false`
// must be caught. naiveReplay cannot discriminate this (it hard-codes the
// same default — see its doc comment and TestReplay_ClearsWithoutPostcondition,
// which asserts the expected state directly instead of via the oracle).
func noPostconditionFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "r6", Seq: next(), Time: at(0)},
		{Kind: KindStepEnter, RunID: "r6", Seq: next(), Time: at(1), Step: "build", Attempt: 1},
		{Kind: KindWrites, RunID: "r6", Seq: next(), Time: at(2), Step: "build", Attempt: 1, Writes: map[string]any{"artifact": "a.bin"}},
		{Kind: KindTransition, RunID: "r6", Seq: next(), Time: at(3), Step: "build", Attempt: 1, Target: "done", Outcome: "success", ViaCatch: false},
		{Kind: KindRunEnd, RunID: "r6", Seq: next(), Time: at(4), Status: "ok"},
	}
}

// interleavedStepPostconditionFixture is round-3 finding G3's regression
// fixture. It is deliberately adversarial, not a realistic engine trace (in
// any real run only one step is ever mid-attempt at a time, so a
// POSTCONDITION always follows its own step's STEP_ENTER before any other
// step's STEP_ENTER can occur) — it exists purely to prove Replay keys
// lastPostconditionOKByStep by the POSTCONDITION event's own Step, not by
// whatever step was most recently entered. "A" enters, then "B" enters
// (moving the "last entered" cursor away from "A"), then "A"'s postcondition
// FAILS, then "A" leaves via a non-catch edge. Correct code must not clear
// "A"'s budget (its postcondition failed); code that mistakenly recorded the
// postcondition result under lastEnter.Step ("B") instead of e.Step ("A")
// would leave "A" at STEP_ENTER's default `true` and wrongly clear it.
func interleavedStepPostconditionFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "r7", Seq: next(), Time: at(0)},
		{Kind: KindStepEnter, RunID: "r7", Seq: next(), Time: at(1), Step: "A", Attempt: 1},
		{Kind: KindStepEnter, RunID: "r7", Seq: next(), Time: at(2), Step: "B", Attempt: 1},
		{Kind: KindPostcondition, RunID: "r7", Seq: next(), Time: at(3), Step: "A", Attempt: 1, OK: false, Text: "boom", AttemptKey: "hZ"},
		{Kind: KindTransition, RunID: "r7", Seq: next(), Time: at(4), Step: "A", Attempt: 1, Target: "done", Outcome: "success", ViaCatch: false},
	}
}

// retryDoesNotInflateVisitsFixture is finding C1's regression fixture: a
// step retried twice within its first visit (three STEP_ENTERs, the last two
// carrying Retry: true) exhausts its attempts: budget and leaves via catch
// to "recover", which routes straight back for a second visit — a fresh
// arrival by an edge (Retry: false) — where it is retried once more before
// passing. Five STEP_ENTERs for "loop" in total, but only two visits: a
// pre-C1 Replay (which counted every STEP_ENTER as a visit regardless of
// Retry) would report Visits["loop"] = 5, which is both wrong in the
// "too many visits arrived later" direction and — because the engine checks
// max_visits: only once per visit, at the top of a retry loop — a step
// could burn through max_visits: worth of *attempts* before the cap ever
// saw a second visit arrive, i.e. simultaneously over- and under-enforced.
func retryDoesNotInflateVisitsFixture() []Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	seq := 0
	next := func() int { seq++; return seq - 1 }

	return []Event{
		{Kind: KindRunStart, RunID: "r8", Seq: next(), Time: at(0)},
		// Visit 1: three tries under the same key, all failing, then
		// exhausted via catch.
		{Kind: KindStepEnter, RunID: "r8", Seq: next(), Time: at(1), Step: "loop", Attempt: 1},
		{Kind: KindPostcondition, RunID: "r8", Seq: next(), Time: at(2), Step: "loop", Attempt: 1, OK: false, Text: "still bad", AttemptKey: "hK"},
		{Kind: KindStepEnter, RunID: "r8", Seq: next(), Time: at(3), Step: "loop", Attempt: 2, AttemptKey: "hK", Retry: true},
		{Kind: KindPostcondition, RunID: "r8", Seq: next(), Time: at(4), Step: "loop", Attempt: 2, OK: false, Text: "still bad", AttemptKey: "hK"},
		{Kind: KindStepEnter, RunID: "r8", Seq: next(), Time: at(5), Step: "loop", Attempt: 3, AttemptKey: "hK", Retry: true},
		{Kind: KindPostcondition, RunID: "r8", Seq: next(), Time: at(6), Step: "loop", Attempt: 3, OK: false, Text: "still bad", AttemptKey: "hK"},
		{Kind: KindTransition, RunID: "r8", Seq: next(), Time: at(7), Step: "loop", Attempt: 3, Target: "recover", Outcome: "failure", ViaCatch: true},
		{Kind: KindStepEnter, RunID: "r8", Seq: next(), Time: at(8), Step: "recover", Attempt: 1},
		{Kind: KindTransition, RunID: "r8", Seq: next(), Time: at(9), Step: "recover", Attempt: 1, Target: "loop", Outcome: "success", ViaCatch: false},
		// Visit 2: a fresh arrival (Retry: false), retried once, then passes.
		{Kind: KindStepEnter, RunID: "r8", Seq: next(), Time: at(10), Step: "loop", Attempt: 1},
		{Kind: KindPostcondition, RunID: "r8", Seq: next(), Time: at(11), Step: "loop", Attempt: 1, OK: false, Text: "still bad", AttemptKey: "hK"},
		{Kind: KindStepEnter, RunID: "r8", Seq: next(), Time: at(12), Step: "loop", Attempt: 4, AttemptKey: "hK", Retry: true},
		{Kind: KindPostcondition, RunID: "r8", Seq: next(), Time: at(13), Step: "loop", Attempt: 4, OK: true},
		{Kind: KindTransition, RunID: "r8", Seq: next(), Time: at(14), Step: "loop", Attempt: 4, Target: "done", Outcome: "success", ViaCatch: false},
		{Kind: KindRunEnd, RunID: "r8", Seq: next(), Time: at(15), Status: "ok"},
	}
}

func ref(step, key string) AttemptRef { return AttemptRef{Step: step, Key: key} }

// wantCompletedRunStates is the C1 fix's headline artefact: a hand-computed
// table of prefix length -> expected RunState for completedRunFixture,
// asserted exactly (not just self-consistently) per prefix. Unlisted map
// fields default to empty, matching Replay's zero-valued maps.
func wantCompletedRunStates() map[int]*RunState {
	return map[int]*RunState{
		0: {Args: map[string]any{}, State: map[string]any{}, Attempts: map[AttemptRef]int{}, Visits: map[string]int{},
			PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{}},
		1: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{}, Attempts: map[AttemptRef]int{}, Visits: map[string]int{},
			PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{}},
		2: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{}, Visits: map[string]int{"build": 1},
			Attempts: map[AttemptRef]int{ref("build", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "build", Attempt: 1}},
		3: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin"}, Visits: map[string]int{"build": 1},
			Attempts: map[AttemptRef]int{ref("build", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "build", Attempt: 1}},
		4: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin"}, Visits: map[string]int{"build": 1},
			Attempts: map[AttemptRef]int{ref("build", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "build", Attempt: 1}},
		5: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin"}, Visits: map[string]int{"build": 1},
			Attempts: map[AttemptRef]int{}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "test", Attempt: 1}},
		6: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin"}, Visits: map[string]int{"build": 1, "test": 1},
			Attempts: map[AttemptRef]int{ref("test", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "test", Attempt: 1}},
		7: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin"}, Visits: map[string]int{"build": 1, "test": 1},
			Attempts: map[AttemptRef]int{ref("test", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "test", Attempt: 1}, LastError: "1 failure"},
		8: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin"}, Visits: map[string]int{"build": 1, "test": 1},
			Attempts: map[AttemptRef]int{ref("test", ""): 1, ref("test", "h1"): 2}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "test", Attempt: 2, AttemptKey: "h1"}, LastError: "1 failure", AttemptLastError: "1 failure"},
		9: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin", "tests_passed": true}, Visits: map[string]int{"build": 1, "test": 1},
			Attempts: map[AttemptRef]int{ref("test", ""): 1, ref("test", "h1"): 2}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "test", Attempt: 2, AttemptKey: "h1"}, LastError: "1 failure", AttemptLastError: "1 failure"},
		10: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin", "tests_passed": true}, Visits: map[string]int{"build": 1, "test": 1},
			Attempts: map[AttemptRef]int{ref("test", ""): 1, ref("test", "h1"): 2}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "test", Attempt: 2, AttemptKey: "h1"}, LastError: "", AttemptLastError: "1 failure"},
		11: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin", "tests_passed": true}, Visits: map[string]int{"build": 1, "test": 1},
			Attempts: map[AttemptRef]int{ref("test", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "done", Attempt: 1}, LastError: "", AttemptLastError: "1 failure"},
		12: {Args: map[string]any{"branch": "feat/x"}, State: map[string]any{"artifact": "a.bin", "tests_passed": true}, Visits: map[string]int{"build": 1, "test": 1},
			Attempts: map[AttemptRef]int{ref("test", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "done", Attempt: 1}, LastError: "", AttemptLastError: "1 failure",
			Ended: true, EndStatus: "ok"},
	}
}

func wantBlockedRunStates() map[int]*RunState {
	return map[int]*RunState{
		0: {Args: map[string]any{}, State: map[string]any{}, Attempts: map[AttemptRef]int{}, Visits: map[string]int{},
			PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{}},
		1: {Args: map[string]any{}, State: map[string]any{}, Attempts: map[AttemptRef]int{}, Visits: map[string]int{},
			PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{}},
		2: {Args: map[string]any{}, State: map[string]any{}, Visits: map[string]int{"deploy": 1},
			Attempts: map[AttemptRef]int{ref("deploy", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "deploy", Attempt: 1}},
		3: {Args: map[string]any{}, State: map[string]any{"url": "https://x"}, Visits: map[string]int{"deploy": 1},
			Attempts: map[AttemptRef]int{ref("deploy", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "deploy", Attempt: 1}},
		4: {Args: map[string]any{}, State: map[string]any{"url": "https://x"}, Visits: map[string]int{"deploy": 1},
			Attempts: map[AttemptRef]int{ref("deploy", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "deploy", Attempt: 1}},
		5: {Args: map[string]any{}, State: map[string]any{"url": "https://x"}, Visits: map[string]int{"deploy": 1},
			Attempts: map[AttemptRef]int{ref("deploy", ""): 1}, PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{}, Cursor: Cursor{Step: "deploy", Attempt: 1}, BlockedReason: "vcs-mutated-outside-guard",
			Ended: true, EndStatus: "blocked", EndReason: "vcs-mutated-outside-guard"},
	}
}

func TestReplay_ExpectedStateTable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture []Event
		want    map[int]*RunState
	}{
		{"completed", completedRunFixture(), wantCompletedRunStates()},
		{"blocked", blockedRunFixture(), wantBlockedRunStates()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for n := 0; n <= len(tc.fixture); n++ {
				want, ok := tc.want[n]
				if !ok {
					t.Fatalf("no expected state hard-coded for prefix length %d — table is incomplete", n)
				}
				got, err := Replay(tc.fixture[:n])
				if err != nil {
					t.Fatalf("Replay(prefix %d): %v", n, err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("prefix %d:\n got  %+v\n want %+v", n, got, want)
				}
			}
		})
	}
}

// naiveReplay is an independently written, deliberately different second
// implementation of Replay's state reconstruction: it does not fold forward
// incrementally tracking "have I transitioned since the last enter" flags,
// it scans backward from the end of the slice for the last STEP_ENTER/RESUME
// and asks whether any TRANSITION follows it, and it recomputes Attempts
// from scratch at the end (an entry is present unless the step's most
// recent entry is followed by a non-catch TRANSITION) rather than
// maintaining it incrementally. It exists purely as a differential oracle
// for TestReplay_ResumeProperty: two independently wrong implementations
// agreeing on every prefix of two real fixtures would be a remarkable
// coincidence.
func naiveReplay(events []Event) (*RunState, error) {
	for i := 1; i < len(events); i++ {
		if events[i].Seq != events[i-1].Seq+1 {
			return nil, fmt.Errorf("naive: seq gap/repeat at %d", i)
		}
	}

	rs := &RunState{
		Args: map[string]any{}, State: map[string]any{},
		Attempts: map[AttemptRef]int{}, Visits: map[string]int{},
		PendingBranches: map[string]map[string]bool{}, BranchOutcome: map[string]string{},
	}

	for _, e := range events {
		switch e.Kind {
		case KindRunStart:
			for k, v := range e.Args {
				rs.Args[k] = v
			}
		case KindStepEnter, KindResume:
			// AttemptLastError: recomputed independently from Replay's
			// running-field approach by re-deriving it fresh on every
			// HardRetry: 0 entry (the start of an attempt) rather than
			// carrying a dedicated "frozen" flag forward — it is simply
			// re-set to whatever LastError already folded up to by this
			// point in the scan, same as Replay, but recomputed here rather
			// than shared code.
			if e.HardRetry == 0 {
				rs.AttemptLastError = rs.LastError
			}
			rs.Attempts[AttemptRef{e.Step, e.AttemptKey}] = e.Attempt
			if e.Kind == KindStepEnter {
				// C1: a retry re-runs the step it is already on, not a
				// fresh arrival by an edge, so it is not a "visit"
				// (design/format-spec.md §B.4).
				if !e.Retry {
					rs.Visits[e.Step]++
				}
			} else if rs.Ended && rs.EndStatus == "blocked" {
				rs.Ended, rs.EndStatus = false, ""
			}
		case KindWrites:
			for k, v := range e.Writes {
				rs.State[k] = v
			}
		case KindPostcondition:
			if e.OK {
				rs.LastError = ""
			} else {
				rs.LastError = e.Text
			}
		case KindRunEnd:
			rs.Ended, rs.EndStatus, rs.EndReason, rs.EndNote = true, e.Status, e.Reason, e.Note
			if e.Status == "blocked" {
				rs.BlockedReason = e.Reason
			}
		}
	}

	// Recompute clearing: for every step that has ever entered, find its
	// last entry index and check whether a non-catch TRANSITION for that
	// step occurs anywhere after it; if so, that entry's ref is not live.
	lastEnterIdx := map[string]int{}
	lastEnterKey := map[string]string{}
	for i, e := range events {
		if e.Kind == KindStepEnter || e.Kind == KindResume {
			lastEnterIdx[e.Step] = i
			lastEnterKey[e.Step] = e.AttemptKey
		}
	}
	for step, idx := range lastEnterIdx {
		for j := idx + 1; j < len(events); j++ {
			if events[j].Kind != KindTransition || events[j].Step != step {
				continue
			}
			// §B.4's clearing rule is a conjunction: passed AND non-catch.
			// Recompute "passed" from scratch by looking at whatever
			// POSTCONDITION (if any) for this step preceded this
			// transition, rather than tracking it incrementally — a
			// deliberately different derivation from Replay's running flag.
			passed := true
			for k := idx + 1; k < j; k++ {
				if events[k].Kind == KindPostcondition && events[k].Step == step {
					passed = events[k].OK
				}
			}
			if passed && !events[j].ViaCatch {
				delete(rs.Attempts, AttemptRef{step, lastEnterKey[step]})
			}
		}
	}

	// Cursor: backward scan for the last STEP_ENTER/RESUME and whatever
	// TRANSITION (if any) comes after it.
	lastEnterAt := -1
	var lastEnter Cursor
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == KindStepEnter || events[i].Kind == KindResume {
			lastEnterAt = i
			lastEnter = Cursor{Step: events[i].Step, Attempt: events[i].Attempt, AttemptKey: events[i].AttemptKey}
			break
		}
	}
	var transitionAfter *Event
	for i := lastEnterAt + 1; i < len(events); i++ {
		if events[i].Kind == KindTransition {
			e := events[i]
			transitionAfter = &e
		}
	}

	blockedStep, blockedOK := "", false
	if len(events) > 0 {
		last := events[len(events)-1]
		if last.Kind == KindRunEnd && last.Status == "blocked" {
			blockedOK = true
			blockedStep = last.Step
			if blockedStep == "" {
				blockedStep = lastEnter.Step
			}
		}
	}

	switch {
	case blockedOK:
		key := lastEnterKey[blockedStep]
		rs.Cursor = Cursor{Step: blockedStep, Attempt: rs.Attempts[AttemptRef{blockedStep, key}], AttemptKey: key}
	case transitionAfter != nil:
		rs.Cursor = Cursor{Step: transitionAfter.Target, Attempt: 1}
	case lastEnterAt >= 0:
		rs.Cursor = lastEnter
	default:
		rs.Cursor = Cursor{}
	}

	return rs, nil
}

// TestReplay_CursorAttemptContract pins I7's contract, spelled out on
// Replay's doc comment: after a TRANSITION, Cursor.Attempt is always 1 and
// Cursor.AttemptKey is always "" — even when the target step has a real,
// nonzero history in rs.Attempts from an earlier visit (a loop). Cursor
// describes what STEP_ENTER will look like once it happens, and no attempt
// key is known for that step until the engine computes one at that moment;
// rs.Attempts still holds the earlier visit's real number under its own
// (step, key), so no information is lost — it is just not the cursor's to
// report before that STEP_ENTER exists.
func TestReplay_CursorAttemptContract(t *testing.T) {
	full := completedRunFixture()
	// Redirect the final TRANSITION so the run loops back to "build" (which
	// already has a completed, and cleared, attempt history) instead of
	// ending, and drop the trailing RUN_END so the run is still in flight.
	looped := append([]Event{}, full[:len(full)-1]...)
	looped[len(looped)-1].Target = "build"

	rs, err := Replay(looped)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := Cursor{Step: "build", Attempt: 1, AttemptKey: ""}
	if rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v (must not inherit build's earlier attempt 1 under key \"\")", rs.Cursor, want)
	}
}

// TestReplay_CatchEdgePreservesAttemptBudget is round-2 finding N2: a catch
// edge must NOT clear the attempt budget it leaves through — the whole
// point of §B.4's clearing rule is that only a non-catch edge ends it.
//
// Verified empirically per the round-2 instructions: with `if !e.ViaCatch`
// mutated to `if true` in Replay, this test failed (Attempts no longer
// contained ref("test", "hX")); the file was restored and this test passes
// again.
func TestReplay_CatchEdgePreservesAttemptBudget(t *testing.T) {
	rs, err := Replay(catchEdgeFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	// "test" was entered once, under key "" (no prior failure yet — the
	// postcondition's own AttemptKey "hX" is provenance for what the *next*
	// attempt would use, but attempts exhausted before a second STEP_ENTER
	// ever consumed it), then left via a catch edge without a second entry.
	want := 1
	got, ok := rs.Attempts[ref("test", "")]
	if !ok {
		t.Fatalf("Attempts = %+v, want it to still contain (test, \"\") — a catch edge must preserve the budget", rs.Attempts)
	}
	if got != want {
		t.Errorf("Attempts[test,\"\"] = %d, want %d", got, want)
	}
}

// TestReplay_BlockedResumeAttemptAboveOne is round-2 finding N3: the
// blocked-resume cursor's attempt must be the step's real attempt number,
// not hard-coded to 1 — blockedRunFixture alone cannot show this, since it
// happens to block on the step's first attempt.
//
// Verified empirically per the round-2 instructions: with the blocked-resume
// branch's Cursor construction hard-coded to Attempt: 1, this test failed
// (got Attempt 1, wanted 2); the file was restored and this test passes
// again.
func TestReplay_BlockedResumeAttemptAboveOne(t *testing.T) {
	rs, err := Replay(blockedAtHigherAttemptFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := Cursor{Step: "deploy", Attempt: 2, AttemptKey: "hA"}
	if rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v", rs.Cursor, want)
	}
}

// TestReplay_CatchEdgeAfterPassPreservesBudget is round-3 finding G1: a
// catch edge taken after a PASSING postcondition must still preserve the
// attempt budget — deleting the `!e.ViaCatch` conjunct entirely (leaving
// only the postcondition-passed check) would wrongly clear it here, unlike
// in catchEdgeFixture where the two conjuncts happen to move together.
//
// Verified empirically per the round-3 instructions: with the clearing
// condition changed from `!e.ViaCatch && lastPostconditionOKByStep[e.Step]`
// to `lastPostconditionOKByStep[e.Step]` (dropping the ViaCatch conjunct),
// this test failed (Attempts no longer contained ref("test", "")); the file
// was restored and this test passes again.
func TestReplay_CatchEdgeAfterPassPreservesBudget(t *testing.T) {
	rs, err := Replay(catchEdgeAfterPassFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	got, ok := rs.Attempts[ref("test", "")]
	if !ok {
		t.Fatalf("Attempts = %+v, want it to still contain (test, \"\") — a catch edge after a passing postcondition must still preserve the budget", rs.Attempts)
	}
	if got != 1 {
		t.Errorf("Attempts[test,\"\"] = %d, want 1", got)
	}
}

// TestReplay_ClearsWithoutPostcondition is round-3 finding G2: a step with
// no POSTCONDITION event at all (legitimate per §B.7 for deterministic/wait/
// human) that leaves by a non-catch edge must still have its budget cleared
// — the STEP_ENTER-time `= true` default is what makes that happen, and
// nothing here ever emits a POSTCONDITION to set it any other way.
//
// This is asserted directly against Replay's own output rather than via
// naiveReplay: the oracle hard-codes the identical `true` default (see its
// doc comment), so a flip of that default in Replay would not show up as a
// disagreement — the two implementations would be identically wrong
// together. An explicit expected value is the only thing that can catch it.
//
// Verified empirically per the round-3 instructions: with the STEP_ENTER
// case's `lastPostconditionOKByStep[e.Step] = true` changed to `= false`,
// this test failed (Attempts still contained ref("build", "")); the file was
// restored and this test passes again.
func TestReplay_ClearsWithoutPostcondition(t *testing.T) {
	rs, err := Replay(noPostconditionFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if _, ok := rs.Attempts[ref("build", "")]; ok {
		t.Errorf("Attempts = %+v, want (build, \"\") cleared: no postcondition at all is vacuously a pass, and the edge taken was non-catch", rs.Attempts)
	}
}

// TestReplay_PostconditionKeyedByOwnStepNotLastEntered is round-3 finding
// G3: the POSTCONDITION case must record its result under the event's own
// Step, never under whatever step was most recently entered. See
// interleavedStepPostconditionFixture's doc comment for why this is
// deliberately adversarial rather than a realistic trace.
//
// Verified empirically per the round-3 instructions: with the POSTCONDITION
// case's `lastPostconditionOKByStep[e.Step] = e.OK` changed to key by
// `lastEnter.Step` instead, this test failed (Attempts no longer contained
// ref("A", "hZ") — "A"'s failed postcondition got recorded under "B" and "A"
// was wrongly cleared); the file was restored and this test passes again.
func TestReplay_PostconditionKeyedByOwnStepNotLastEntered(t *testing.T) {
	rs, err := Replay(interleavedStepPostconditionFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	got, ok := rs.Attempts[ref("A", "")]
	if !ok {
		t.Fatalf("Attempts = %+v, want it to still contain (A, \"\") — A's own failed postcondition must block the clear, regardless of B having been entered in between", rs.Attempts)
	}
	if got != 1 {
		t.Errorf("Attempts[A,\"\"] = %d, want 1", got)
	}
}

// TestReplay_RetryDoesNotCountAsVisit is finding C1's direct expected-state
// assertion: retryDoesNotInflateVisitsFixture has five STEP_ENTERs for
// "loop" across two visits (three tries in the first, two in the second),
// but Visits["loop"] must be 2 — one per visit, not one per try — and
// Visits["recover"] must be 1. This is checked directly against the exact
// expected map, not just self-consistently against naiveReplay, per the
// round-1 controller's condition that new journal behaviour needs both.
func TestReplay_RetryDoesNotCountAsVisit(t *testing.T) {
	rs, err := Replay(retryDoesNotInflateVisitsFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := map[string]int{"loop": 2, "recover": 1}
	if !reflect.DeepEqual(rs.Visits, want) {
		t.Errorf("Visits = %v, want %v (five STEP_ENTERs for loop, but only two are fresh visits)", rs.Visits, want)
	}
}

// TestReplay_HardRetryDoesNotCountAsVisit: a STEP_ENTER with HardRetry > 0
// (design/format-spec.md §B.16's retry:, distinct from the Retry field
// above, which marks an attempts:-level re-entry) must not be counted as a
// visit either — retries consume neither attempts: nor max_visits:.
func TestReplay_HardRetryDoesNotCountAsVisit(t *testing.T) {
	events := []Event{
		{Kind: KindRunStart, Seq: 0},
		{Kind: KindStepEnter, Seq: 1, Step: "a", Attempt: 1},
		{Kind: KindStepEnter, Seq: 2, Step: "a", Attempt: 1, HardRetry: 1},
		{Kind: KindStepEnter, Seq: 3, Step: "a", Attempt: 1, HardRetry: 2},
		{Kind: KindTransition, Seq: 4, Step: "a", Target: "b"},
		{Kind: KindStepEnter, Seq: 5, Step: "b", Attempt: 1},
	}
	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := map[string]int{"a": 1, "b": 1}
	if !reflect.DeepEqual(rs.Visits, want) {
		t.Errorf("Visits = %v, want %v (three STEP_ENTERs for a, but only the first is a fresh visit)", rs.Visits, want)
	}
}

// TestReplay_CursorCarriesHardRetry: a crash-resume cursor (no TRANSITION
// since the last STEP_ENTER) must carry that STEP_ENTER's HardRetry through,
// so the engine can continue a hard-retry loop at the same count instead of
// restarting it (see internal/engine/deterministic.go).
func TestReplay_CursorCarriesHardRetry(t *testing.T) {
	events := []Event{
		{Kind: KindRunStart, Seq: 0},
		{Kind: KindStepEnter, Seq: 1, Step: "a", Attempt: 1},
		{Kind: KindStepEnter, Seq: 2, Step: "a", Attempt: 1, HardRetry: 1},
	}
	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := Cursor{Step: "a", Attempt: 1, AttemptKey: "", HardRetry: 1}
	if rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v", rs.Cursor, want)
	}
}

func TestReplay_CrashResume_MidStepNeverTransitioned(t *testing.T) {
	full := completedRunFixture()
	prefix := full[:8] // up to and including StepEnter test/2
	rs, err := Replay(prefix)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := Cursor{Step: "test", Attempt: 2, AttemptKey: "h1"}
	if rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v", rs.Cursor, want)
	}
}

func TestReplay_CrashResume_AfterTransition(t *testing.T) {
	full := completedRunFixture()
	prefix := full[:5]
	rs, err := Replay(prefix)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := Cursor{Step: "test", Attempt: 1}
	if rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v", rs.Cursor, want)
	}
}

func TestReplay_BlockedResume(t *testing.T) {
	rs, err := Replay(blockedRunFixture())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := Cursor{Step: "deploy", Attempt: 1}
	if rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v", rs.Cursor, want)
	}
	if rs.Status() != "blocked" {
		t.Errorf("Status() = %q, want blocked", rs.Status())
	}
	if rs.Terminal() {
		t.Error("Terminal() = true, want false: blocked is not terminal")
	}
	if rs.BlockedReason != "vcs-mutated-outside-guard" {
		t.Errorf("BlockedReason = %q", rs.BlockedReason)
	}
}

// TestReplay_BlockedResume_InterventionRestoresRunning is I8: nothing
// previously tested that a RESUME{intervention:true, attempt:1} appended
// after a blocked RUN_END replays to a running cursor at attempt 1 — the
// contract the doc comment on Replay describes but that only the caller
// (the engine, appending the RESUME event and replaying again) can
// exercise for real. It is not Replay's job to reset the attempt itself
// (purity), only to react correctly once the reset has been journalled.
func TestReplay_BlockedResume_InterventionRestoresRunning(t *testing.T) {
	events := append(append([]Event{}, blockedRunFixture()...), Event{
		Kind: KindResume, RunID: "r2", Seq: 5, Step: "deploy", Attempt: 1, Intervention: true,
	})
	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if rs.Ended {
		t.Error("Ended = true after an intervention RESUME, want false: the run is running again")
	}
	want := Cursor{Step: "deploy", Attempt: 1}
	if rs.Cursor != want {
		t.Errorf("Cursor = %+v, want %+v", rs.Cursor, want)
	}
}

// TestReplay_StrayResumeDoesNotResurrectATerminalRun is I9: a RESUME event
// appearing after a truly terminal RUN_END (status "ok", not "blocked")
// must not flip Ended back to false — that would put a finished run back
// into Live()'s results. A RESUME only legitimately un-ends a run that was
// BLOCKED.
func TestReplay_StrayResumeDoesNotResurrectATerminalRun(t *testing.T) {
	events := append(append([]Event{}, completedRunFixture()...), Event{
		Kind: KindResume, RunID: "r1", Seq: 12, Step: "done", Attempt: 1,
	})
	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !rs.Ended || rs.EndStatus != "ok" {
		t.Errorf("Ended/EndStatus = %v/%q after a stray RESUME, want true/\"ok\" (still terminal)", rs.Ended, rs.EndStatus)
	}
	if !rs.Terminal() {
		t.Error("Terminal() = false after a stray RESUME on a done run, want true")
	}
}

func TestReplay_Empty(t *testing.T) {
	rs, err := Replay(nil)
	if err != nil {
		t.Fatalf("Replay(nil): %v", err)
	}
	if rs.Ended || rs.Cursor != (Cursor{}) {
		t.Errorf("Replay(nil) = %+v, want zero-ish", rs)
	}
}

func TestReplay_UnknownKind(t *testing.T) {
	_, err := Replay([]Event{{Kind: "NOT_A_KIND", Seq: 0}})
	if err == nil {
		t.Fatal("Replay: want error for an unknown kind")
	}
}

func TestReplay_SeqGap(t *testing.T) {
	_, err := Replay([]Event{{Kind: KindRunStart, Seq: 0}, {Kind: KindStepEnter, Seq: 2, Step: "s"}})
	if err == nil {
		t.Fatal("Replay: want error for a sequence gap")
	}
}

func TestReplay_SeqRepeat(t *testing.T) {
	_, err := Replay([]Event{{Kind: KindRunStart, Seq: 0}, {Kind: KindStepEnter, Seq: 0, Step: "s"}})
	if err == nil {
		t.Fatal("Replay: want error for a repeated sequence number")
	}
}

// TestReplay_ResumeProperty is the property test DESIGN.md §6 asks for by
// name. Unlike its round-1 version — which asserted only Replay(p)==Replay(p)
// and Replay(longer[:n])==Replay(p), both tautologies for any deterministic
// pure function and therefore vacuous — this checks Replay's output against
// naiveReplay, an independently coded second implementation (see its doc
// comment), for every prefix of two real fixtures. TestReplay_ExpectedStateTable
// covers the same ground against a hand-computed table instead of a second
// implementation; between the two, a wrong Replay has nowhere to hide.
//
// Checked empirically: a deliberately broken Replay (STEP_ENTER counting
// Visits[e.Step] += 2, and the transitionedSinceEnter branch hard-coding
// Cursor.Attempt to 99) was run against this test and against
// TestReplay_ExpectedStateTable; both failed immediately, on the first
// prefix the mutation affected, with a diff naming the wrong field. The
// round-1 version of this test passed unchanged against the same mutations.
func TestReplay_ResumeProperty(t *testing.T) {
	fixtures := map[string][]Event{
		"completed":                 completedRunFixture(),
		"blocked":                   blockedRunFixture(),
		"catch-edge":                catchEdgeFixture(),
		"blocked-at-attempt-2":      blockedAtHigherAttemptFixture(),
		"catch-edge-after-pass":     catchEdgeAfterPassFixture(),
		"no-postcondition":          noPostconditionFixture(),
		"interleaved-postcondition": interleavedStepPostconditionFixture(),
		"retry-does-not-inflate":    retryDoesNotInflateVisitsFixture(),
	}
	names := []string{
		"completed", "blocked", "catch-edge", "blocked-at-attempt-2",
		"catch-edge-after-pass", "no-postcondition", "interleaved-postcondition",
		"retry-does-not-inflate",
	}
	for _, name := range names {
		full := fixtures[name]
		for n := 0; n <= len(full); n++ {
			prefix := full[:n]

			got, err := Replay(prefix)
			if err != nil {
				t.Fatalf("%s: Replay(prefix %d): %v", name, n, err)
			}
			want, err := naiveReplay(prefix)
			if err != nil {
				t.Fatalf("%s: naiveReplay(prefix %d): %v", name, n, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: prefix %d: Replay and the independent oracle disagree:\n Replay: %+v\n oracle: %+v", name, n, got, want)
			}
		}
	}
}

// TestReplay_EnforcementOptOut: RUN_START's hook_self_test is what pawl run
// decided about enforcement for this run, and the run directory's only
// persistent record of an opt-out — the hooks read it back through Replay
// to leave an opted-out run alone.
func TestReplay_EnforcementOptOut(t *testing.T) {
	for _, tc := range []struct {
		label string
		off   bool
	}{
		{"off (--no-enforcement)", true},
		{"off (PAWL_ENFORCEMENT=off)", true},
		{"on (PreToolUse heartbeat)", false},
		{"unknown", false},
		{"", false},
	} {
		rs, err := Replay([]Event{{Kind: KindRunStart, RunID: "r1", Seq: 0, HookSelfTest: tc.label}})
		if err != nil {
			t.Fatal(err)
		}
		if rs.Enforcement != tc.label || rs.EnforcementOff() != tc.off {
			t.Fatalf("%q: Enforcement=%q EnforcementOff=%v, want off=%v", tc.label, rs.Enforcement, rs.EnforcementOff(), tc.off)
		}
	}
}
