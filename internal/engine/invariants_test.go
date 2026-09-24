package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// TestInvariant_HoldsRunContinues: a declared invariant that always holds
// (exit 0) never blocks the run.
func TestInvariant_HoldsRunContinues(t *testing.T) {
	const yaml = `
workflow: invariant-holds
start: a
invariants:
  - id: always-holds
    check: "true"
    message: "should never fire"
steps:
  - id: a
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
		t.Fatalf("got %+v, want Terminal{ok}: a holding invariant must not block the run", instr)
	}
}

// TestInvariant_ViolatedAfterDeterministicStep: a violated invariant blocks
// the run right after the deterministic step that just completed, with the
// RUN_END carrying the invariant's id and message, and the route the step
// would otherwise have taken (to "done") never taken (no TRANSITION
// journaled for it — point 2: the violation pre-empts even a route to a
// terminal).
func TestInvariant_ViolatedAfterDeterministicStep(t *testing.T) {
	const yaml = `
workflow: invariant-violated-det
start: a
invariants:
  - id: never-holds
    check: "false"
    message: "the world changed underneath the run"
steps:
  - id: a
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
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}", instr)
	}
	if term.StepID != "a" {
		t.Errorf("Terminal.StepID = %q, want %q (the step that just completed)", term.StepID, "a")
	}
	wantMsg := `invariant "never-holds" violated: the world changed underneath the run`
	if term.Message != wantMsg {
		t.Errorf("Terminal.Message = %q, want %q", term.Message, wantMsg)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == journal.KindTransition {
			t.Fatalf("a TRANSITION was journaled (%+v); the invariant violation must pre-empt it entirely", ev)
		}
	}
	var sawRunEnd bool
	for _, ev := range events {
		if ev.Kind == journal.KindRunEnd {
			sawRunEnd = true
			if ev.Status != "blocked" || ev.Step != "a" || ev.Reason != wantMsg {
				t.Errorf("RUN_END = %+v, want {status: blocked, step: a, reason: %q}", ev, wantMsg)
			}
		}
	}
	if !sawRunEnd {
		t.Fatal("no RUN_END event journaled")
	}
}

// TestInvariant_ViolatedPreemptsDoneRoute: same as above, but the step's own
// route goes straight to the implicit "done" terminal — the violation still
// pre-empts it (design/format-spec.md §B.12).
func TestInvariant_ViolatedPreemptsDoneRoute(t *testing.T) {
	const yaml = `
workflow: invariant-preempts-done
start: a
invariants:
  - id: never-holds
    check: "false"
    message: "blocked before done"
steps:
  - id: a
    kind: deterministic
    run: "true"
    next: done
terminal: {done: {status: ok, message: "should never be reached"}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}, not the done terminal", instr)
	}
}

// TestInvariant_CheckCannotRunCountsAsViolated: a check: naming a missing
// script counts as violated, never as a thrown Go error (design/format-spec.md
// §10, docs/guards-and-invariants.md).
func TestInvariant_CheckCannotRunCountsAsViolated(t *testing.T) {
	const yaml = `
workflow: invariant-missing-script
start: a
invariants:
  - id: unreachable-script
    check: scripts/does-not-exist.sh
    message: "the check itself could not run"
steps:
  - id: a
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
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}: a check that cannot run counts as violated", instr)
	}
}

// TestInvariant_ViolatedAfterAgenticSubmit: an invariant is evaluated after
// pawl submit for an agentic step, before its TRANSITION.
func TestInvariant_ViolatedAfterAgenticSubmit(t *testing.T) {
	const yaml = `
workflow: invariant-agentic
start: work
state:
  findings: {type: json, default: []}
invariants:
  - id: never-holds
    check: "false"
    message: "blocked after agentic submit"
steps:
  - id: work
    kind: agentic
    description: "do work"
    writes: {findings: {type: json}}
    postcondition: {all_set: [findings]}
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := instr.(Dispatch); !ok {
		t.Fatalf("got %T, want Dispatch", instr)
	}
	instr, err = e.Submit("run1", "work", 1, []byte(`{"findings": [1]}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}", instr)
	}
}

// TestInvariant_ViolatedAfterHumanSubmit: an invariant is evaluated after
// SubmitHuman, before its TRANSITION.
func TestInvariant_ViolatedAfterHumanSubmit(t *testing.T) {
	const yaml = `
workflow: invariant-human
start: ask
state:
  reviewer: {type: string, default: ""}
invariants:
  - id: never-holds
    check: "false"
    message: "blocked after human submit"
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options: [alice, bob]
    timeout: 24h
    writes: [reviewer]
    outcomes:
      alice: done
      bob: done
      timeout: blocked
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a, ok := instr.(Ask)
	if !ok {
		t.Fatalf("got %T, want Ask", instr)
	}
	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["alice"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}", instr)
	}
}

// TestInvariant_ViolatedAfterWaitCompletion: an invariant is evaluated after
// a wait step's completion (pawl poll's internal submit), before its
// TRANSITION.
func TestInvariant_ViolatedAfterWaitCompletion(t *testing.T) {
	const yaml = `
workflow: invariant-wait
start: w
state:
  build_status: {type: string, default: ""}
invariants:
  - id: never-holds
    check: "false"
    message: "blocked after wait completion"
steps:
  - id: w
    kind: wait
    poll: "echo PASSED"
    every: 1ms
    timeout: 5s
    emits: pairs
    writes: {build_status: {type: string}}
    outcomes:
      PASSED: done
      timeout: blocked
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := instr.(Wait); !ok {
		t.Fatalf("got %T, want Wait", instr)
	}
	instr, err = e.Poll("run1", "w", nil)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}", instr)
	}
}

// TestInvariant_BlockedResumeRerunsViolatingStep: resuming a run blocked by
// an invariant lands back on the step that produced the blocked outcome
// (RUN_END.Step, via journal.Replay's existing blocked-resume cursor
// mechanism — no special-casing needed anywhere), with that step's attempt
// counter reset to 1. Once the invariant's check: passes (an external state
// change flips it), the run continues past it.
//
// The invariant's check: substitutes a run-scoped ${key} (an arg naming an
// absolute path) — proving the check runs against the state as of the
// just-completed step, not some cached earlier snapshot.
func TestInvariant_BlockedResumeRerunsViolatingStep(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	const yaml = `
workflow: invariant-resume
start: a
args:
  marker_path: {type: string, required: true}
steps:
  - id: a
    kind: deterministic
    run: "true"
    attempts: 3
    next: done
terminal: {done: {status: ok}}
invariants:
  - id: marker-exists
    check: test -f ${marker_path}
    message: "the marker file is missing"
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", map[string]any{"marker_path": marker})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}: the marker file does not exist yet", instr)
	}

	// External state changes: create the marker the invariant's check: now
	// looks for.
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	instr, err = e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	term, ok = instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}: the invariant now holds, so the run should complete", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawIntervention bool
	var interventionAttempt int
	var interventionStep string
	var stepAEnters int
	for _, ev := range events {
		if ev.Kind == journal.KindResume && ev.Intervention {
			sawIntervention = true
			interventionAttempt = ev.Attempt
			interventionStep = ev.Step
		}
		if ev.Kind == journal.KindStepEnter && ev.Step == "a" {
			stepAEnters++
		}
	}
	if !sawIntervention {
		t.Fatal("no RESUME{intervention:true} event appended")
	}
	if interventionStep != "a" {
		t.Errorf("RESUME.Step = %q, want %q (the step the invariant violation blocked at)", interventionStep, "a")
	}
	if interventionAttempt != 1 {
		t.Errorf("RESUME.Attempt = %d, want 1 (the counter resets)", interventionAttempt)
	}
	// Step "a" enters twice: once for the original (blocked) visit, once
	// for the resumed re-run.
	if stepAEnters != 2 {
		t.Errorf("STEP_ENTER count for step a = %d, want 2 (original + resumed re-run)", stepAEnters)
	}
}

// TestInvariant_UsesJustWrittenState: an invariant's check: ${key}
// substitution reflects the state the just-completed step itself wrote
// (WRITES is journaled before invariants are evaluated — point 2).
func TestInvariant_UsesJustWrittenState(t *testing.T) {
	const yaml = `
workflow: invariant-fresh-state
start: a
state:
  status: {type: string, default: "pending"}
invariants:
  - id: status-must-be-ok
    check: test ${status} = ok
    message: "status was not ok"
steps:
  - id: a
    kind: deterministic
    run: "echo status=ok"
    emits: pairs
    writes: {status: {type: string}}
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
		t.Fatalf("got %+v, want Terminal{ok}: the invariant reads the freshly-written status=ok", instr)
	}
}

// TestInvariant_CheckInjectionAttemptDoesNotExecute mirrors
// TestExecDeterministic_InjectionAttemptDoesNotExecute (scriptpath_test.go):
// a state value written by the just-completed step, containing
// "x'/y $(touch PWNED)'" in FIRST-TOKEN position, must not execute when
// substituted into an invariant's check: — proving invariants' check:
// reuses the same resolveScriptPathTemplate/execShell path a postcondition's
// command: does (the decision is made on the unrendered template, never on
// already-shell-quoted output — see scriptpath.go), not a hand-rolled
// substitution.
func TestInvariant_CheckInjectionAttemptDoesNotExecute(t *testing.T) {
	const yaml = `
workflow: invariant-injection-guard
start: work
state:
  payload: {type: string, default: ""}
invariants:
  - id: reobserve
    check: ${payload}
    message: "should never fire cleanly"
steps:
  - id: work
    kind: agentic
    description: "write payload"
    writes: {payload: {type: string}}
    postcondition: {all_set: [payload]}
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := instr.(Dispatch); !ok {
		t.Fatalf("got %T, want Dispatch", instr)
	}

	malicious := "x'/y $(touch PWNED)'"
	// The invariant is expected to violate (there is no such file to
	// execute, so the "command" fails or errors) — what matters is what
	// does NOT happen, checked below.
	instr, err = e.Submit("run1", "work", 1, []byte(`{"payload":"`+malicious+`"}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}: the malicious payload does not name a real command", instr)
	}

	if _, err := os.Stat(filepath.Join(e.Root, "PWNED")); err == nil {
		t.Fatal("SECURITY: the injected $(touch PWNED) executed — command injection via an invariant's check: first token")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.Workflow.Path), "PWNED")); err == nil {
		t.Fatal("SECURITY: the injected $(touch PWNED) executed (in the workflow dir) — command injection via an invariant's check: first token")
	}
}

// TestInvariant_ViolatedAfterParallelBranchSubmit: an invariant is
// evaluated at a kind: parallel step's group-join (routeParallel), once
// every branch has resolved — the one place a violation can be journaled
// without corrupting RunState.PendingBranches or resumeParallel's
// re-derivation of still-outstanding branches (see preTransitionInvariantBlock's
// doc comment for why per-branch pre-emption mid-group is not attempted).
func TestInvariant_ViolatedAfterParallelBranchSubmit(t *testing.T) {
	const yaml = `
workflow: invariant-parallel
start: p
state:
  r1: {type: string, default: ""}
  r2: {type: string, default: ""}
invariants:
  - id: never-holds
    check: "false"
    message: "blocked after the group joined"
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: done
  - id: b1
    kind: deterministic
    run: "true"
    writes: {r1: {type: string}}
  - id: b2
    kind: deterministic
    run: "true"
    writes: {r2: {type: string}}
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}", instr)
	}
	if term.StepID != "p" {
		t.Errorf("Terminal.StepID = %q, want %q (the parallel step, once its group joined)", term.StepID, "p")
	}
}

// TestInvariant_BlockedResumeRerunsParallelGroup: an invariant that reads a
// branch-written state key blocks at a kind: parallel join, then a blocked
// resume must re-run the whole group (not just re-derive the — already
// empty — PendingBranches set) so the branch can produce fresh WRITES the
// invariant can actually pass against. b1's script writes "bad" the first
// time it runs and "ok" every time after (tracked via a counter file),
// mirroring TestInvariant_BlockedResumeRerunsViolatingStep's marker-file
// idiom but for a parallel step's branch.
func TestInvariant_BlockedResumeRerunsParallelGroup(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	yaml := strings.ReplaceAll(`
workflow: invariant-parallel-resume
start: p
state:
  r1: {type: string, default: ""}
  r2: {type: string, default: ""}
invariants:
  - id: r1-must-be-ok
    check: test ${r1} = ok
    message: "r1 not ok yet"
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: done
  - id: b1
    kind: deterministic
    run: sh -c 'if [ -f COUNTER_PATH ]; then echo r1=ok; else touch COUNTER_PATH; echo r1=bad; fi'
    emits: pairs
    writes: {r1: {type: string}}
  - id: b2
    kind: deterministic
    run: "echo r2=y"
    emits: pairs
    writes: {r2: {type: string}}
terminal: {done: {status: ok}}
`, "COUNTER_PATH", counter)
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}: r1=bad on the first run", instr)
	}
	if term.StepID != "p" {
		t.Errorf("Terminal.StepID = %q, want %q", term.StepID, "p")
	}

	instr, err = e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	term, ok = instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}: the resumed group re-ran b1, which now writes r1=ok", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var pEnters, b1GroupedEnters, b2GroupedEnters int
	var sawIntervention bool
	for _, ev := range events {
		if ev.Kind == journal.KindResume && ev.Intervention {
			sawIntervention = true
			if ev.Step != "p" {
				t.Errorf("RESUME.Step = %q, want %q", ev.Step, "p")
			}
		}
		if ev.Kind == journal.KindStepEnter && ev.Step == "p" && ev.Group == "" {
			pEnters++
		}
		if ev.Kind == journal.KindStepEnter && ev.Step == "b1" && ev.Group == "p" {
			b1GroupedEnters++
		}
		if ev.Kind == journal.KindStepEnter && ev.Step == "b2" && ev.Group == "p" {
			b2GroupedEnters++
		}
	}
	if !sawIntervention {
		t.Fatal("no RESUME{intervention:true} event appended")
	}
	if pEnters != 2 {
		t.Errorf("STEP_ENTER count for step p = %d, want 2 (original + resumed re-run)", pEnters)
	}
	if b1GroupedEnters != 2 {
		t.Errorf("grouped STEP_ENTER count for branch b1 = %d, want 2 (the blocked resume must re-run the whole group, not just re-derive an already-empty PendingBranches set)", b1GroupedEnters)
	}
	if b2GroupedEnters != 2 {
		t.Errorf("grouped STEP_ENTER count for branch b2 = %d, want 2", b2GroupedEnters)
	}
}

// TestInvariant_NotEvaluatedDuringRetryLoop: while a step's own
// attempts:-budget retry loop is still going (a postcondition failure with
// budget left to redispatch), invariants are not evaluated — only once the
// step actually completes with a resolved outcome (point 2). Proven by
// counting how many times the invariant's check: actually ran (it appends a
// line to a counter file each time): it must run exactly once, for the
// final (successful) attempt, never for the first attempt that failed its
// own postcondition and retried.
func TestInvariant_NotEvaluatedDuringRetryLoop(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	yaml := strings.ReplaceAll(`
workflow: invariant-retry-skip
start: a
state:
  tries: {type: integer, default: 0}
invariants:
  - id: counts-evaluations
    check: sh -c 'echo x >> COUNTER_PATH'
    message: "unreachable: the check always holds"
steps:
  - id: a
    kind: deterministic
    run: sh -c 'echo tries=$((${tries}+1))'
    emits: pairs
    writes: {tries: {type: integer}}
    postcondition: {equals: {tries: 2}}
    attempts: 3
    next: done
terminal: {done: {status: ok}}
`, "COUNTER_PATH", counter)
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} (tries reaches 2 on the second attempt)", instr)
	}

	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("reading counter file: %v", err)
	}
	lines := strings.Count(string(data), "\n")
	if lines != 1 {
		t.Errorf("invariant check: ran %d time(s), want exactly 1 (only once the step actually completed, not during the failed first attempt's retry)", lines)
	}
}
