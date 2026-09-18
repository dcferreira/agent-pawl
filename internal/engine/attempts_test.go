package engine

import (
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// TestAttempts_PersistedForLifeOfRun: attempts: re-runs the same step on
// postcondition failure, up to the declared budget (design/format-spec.md
// §B.4, first bullet), and the count is visible in the journal across the
// whole run.
func TestAttempts_PersistedForLifeOfRun(t *testing.T) {
	const yaml = `
workflow: attempts-persist
start: flaky
state:
  n: {type: integer, default: 0}
steps:
  - id: flaky
    kind: deterministic
    run: |
      n=$(cat count.txt 2>/dev/null || echo 0)
      n=$((n+1))
      echo "$n" > count.txt
      echo "n=$n"
    emits: pairs
    writes: [n]
    postcondition: '[ "$(cat count.txt)" -ge 3 ]'
    attempts: 5
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want a successful Terminal", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var enters int
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter {
			enters++
		}
	}
	if enters != 3 {
		t.Errorf("STEP_ENTER count = %d, want 3 (attempts 1,2,3 until the postcondition passed)", enters)
	}
}

// TestAttempts_ExhaustionRoutesFailure: exceeding attempts: is a "failure"
// outcome, routed via catch: — the reserved-outcome exemption
// (design/format-spec.md §B.11), not the reserved "exhausted" (which is
// reserved for max_visits:/max_steps:).
func TestAttempts_ExhaustionRoutesFailure(t *testing.T) {
	const yaml = `
workflow: attempts-exhaust
start: always-fails
steps:
  - id: always-fails
    kind: deterministic
    run: "true"
    postcondition: "false"
    attempts: 2
    next: done
    catch: [{on: failure, next: fallback}]
  - id: fallback
    kind: deterministic
    run: "true"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} via the fallback catch route", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var enters int
	var sawFailureTransition bool
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter && ev.Step == "always-fails" {
			enters++
		}
		if ev.Kind == journal.KindTransition && ev.Step == "always-fails" && ev.Outcome == "failure" {
			sawFailureTransition = true
			if !ev.ViaCatch {
				t.Errorf("TRANSITION for exhausted attempts: ViaCatch = false, want true")
			}
			if ev.Target != "fallback" {
				t.Errorf("TRANSITION target = %q, want fallback", ev.Target)
			}
		}
	}
	if enters != 2 {
		t.Errorf("STEP_ENTER count = %d, want 2 (attempts: 2)", enters)
	}
	if !sawFailureTransition {
		t.Error("no TRANSITION{outcome:failure} recorded")
	}
}

// TestAttempts_ClearedOnPassNonCatchEdge: cleared when the postcondition
// passes and the step leaves by a non-catch edge (design/format-spec.md
// §B.4, fourth bullet).
func TestAttempts_ClearedOnPassNonCatchEdge(t *testing.T) {
	const yaml = `
workflow: attempts-clear
start: eventually-ok
steps:
  - id: eventually-ok
    kind: deterministic
    run: |
      n=$(cat count.txt 2>/dev/null || echo 0)
      n=$((n+1))
      echo "$n" > count.txt
    postcondition: '[ "$(cat count.txt)" -ge 2 ]'
    attempts: 3
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var failKey string
	for _, ev := range events {
		if ev.Kind == journal.KindPostcondition && !ev.OK {
			failKey = ev.AttemptKey
		}
	}
	if failKey == "" {
		t.Fatal("no failing POSTCONDITION recorded; test setup is broken")
	}
	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if _, stillThere := rs.Attempts[journal.AttemptRef{Step: "eventually-ok", Key: failKey}]; stillThere {
		t.Errorf("attempt budget for key %q still present after a pass + non-catch exit: %v", failKey, rs.Attempts)
	}
}

// TestAttempts_NotAdvancedByCrash: the re-run after an interruption is the
// same attempt number (design/format-spec.md §B.4, third bullet). This
// constructs a journal by hand to simulate a crash mid-attempt (a STEP_ENTER
// with no following POSTCONDITION/TRANSITION) and asserts Resume re-enters
// at that same attempt, not the next one.
func TestAttempts_NotAdvancedByCrash(t *testing.T) {
	const yaml = `
workflow: crash-resume
start: flaky
steps:
  - id: flaky
    kind: deterministic
    run: "echo bad >&2; false"
    attempts: 5
    next: done
terminal: {done: {status: ok}}
`
	w := loadWorkflow(t, yaml)
	stateDir := t.TempDir()
	t.Setenv(journal.EnvStateDir, stateDir)
	root := t.TempDir()
	e := &Engine{Workflow: w, Root: root, Timeout: 5 * time.Second}

	runID := "run1"
	dir, err := journal.CreateRunDir(root, w.Workflow, runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.WritePlan(dir, w); err != nil {
		t.Fatal(err)
	}
	log, err := journal.OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend := func(ev journal.Event) {
		t.Helper()
		if _, err := log.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	mustAppend(journal.Event{Kind: journal.KindRunStart, RunID: runID})
	mustAppend(journal.Event{Kind: journal.KindStepEnter, RunID: runID, Step: "flaky", Attempt: 1, AttemptKey: ""})
	key := hashFailureText("bad")
	mustAppend(journal.Event{Kind: journal.KindPostcondition, RunID: runID, Step: "flaky", Attempt: 1, OK: false, Text: "bad", AttemptKey: key})
	// Simulate the crash: attempt 2 began (its STEP_ENTER is durable) but
	// nothing about how it ended was ever journaled.
	mustAppend(journal.Event{Kind: journal.KindStepEnter, RunID: runID, Step: "flaky", Attempt: 2, AttemptKey: key})
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Resume(runID, false); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var attempt2Enters int
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter && ev.Step == "flaky" && ev.Attempt == 2 {
			attempt2Enters++
		}
	}
	if attempt2Enters != 2 {
		t.Errorf("STEP_ENTER{flaky, attempt:2} count = %d, want 2 (one before the simulated crash, one from resume) — a crash must not advance the attempt counter", attempt2Enters)
	}
}

// TestAttempts_NotScopedToIncomingEdge: re-entering a step by any edge
// continues the same countdown, provided attempt_key resolves the same
// (design/format-spec.md §B.4, second bullet). The first visit to "check"
// burns its whole attempts: budget (5 tries, same failure text every time);
// every later visit — reached via a different edge (through "fix") — must
// see the budget already spent and fail on its very first try, rather than
// getting a fresh 5 tries of its own.
func TestAttempts_NotScopedToIncomingEdge(t *testing.T) {
	const yaml = `
workflow: not-scoped
start: check
max_steps: 9
steps:
  - id: check
    kind: deterministic
    run: "true"
    postcondition: "echo 'still bad'; exit 1"
    attempts: 5
    max_visits: 8
    next: done
    catch: [{on: failure, next: fix}]
  - id: fix
    kind: deterministic
    run: "true"
    max_visits: 8
    next: check
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} once max_steps: binds", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var totalCheckEnters, bootstrapCheckEnters int
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter && ev.Step == "check" {
			totalCheckEnters++
			if ev.AttemptKey == "" {
				bootstrapCheckEnters++
			}
		}
	}
	if bootstrapCheckEnters < 2 {
		t.Fatalf("check was only visited %d time(s); need at least 2 to exercise cross-visit continuation", bootstrapCheckEnters)
	}
	// Visit 1 spends the full 5-attempt budget; every subsequent visit
	// fails immediately on its bootstrap try (attempt 1) because the
	// budget for the same failure text was already spent — it does not
	// get a fresh countdown just because it arrived via a different edge.
	wantTotal := 5 + (bootstrapCheckEnters - 1)
	if totalCheckEnters != wantTotal {
		t.Errorf("total STEP_ENTER(check) = %d, want %d (5 for the first visit, 1 for each of the other %d visits)",
			totalCheckEnters, wantTotal, bootstrapCheckEnters-1)
	}
}

// TestCaps_MaxVisitsProducesExhausted: exceeding max_visits: produces the
// reserved outcome exhausted on the step that would have been entered
// (design/format-spec.md §B.4, fifth/sixth bullets), and an unrouted
// exhausted goes to blocked (§B.11).
func TestCaps_MaxVisitsProducesExhausted(t *testing.T) {
	const yaml = `
workflow: max-visits
start: loop
steps:
  - id: loop
    kind: deterministic
    run: "echo GO"
    emits: pairs
    max_visits: 3
    outcomes:
      GO: loop
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} (unrouted exhausted)", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var enters int
	var sawExhausted bool
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter {
			enters++
		}
		if ev.Kind == journal.KindTransition && ev.Outcome == "exhausted" {
			sawExhausted = true
			if ev.Target != "blocked" || !ev.ViaCatch {
				t.Errorf("exhausted TRANSITION = %+v, want target blocked, viaCatch true", ev)
			}
		}
	}
	if enters != 3 {
		t.Errorf("STEP_ENTER count = %d, want exactly max_visits (3): the 4th entry is never made", enters)
	}
	if !sawExhausted {
		t.Error("no TRANSITION{outcome:exhausted} recorded")
	}
}
