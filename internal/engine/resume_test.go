package engine

import (
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// TestResume_BlockedInterventionResetsAttempt: resuming a BLOCKED run
// appends RESUME{intervention: true}, which resets the step's attempt
// counter to 1 (design/format-spec.md §B.12, DESIGN.md §4 step 4) — the
// append is this package's job, per the ruling it owns; Replay does not
// synthesise it.
func TestResume_BlockedInterventionResetsAttempt(t *testing.T) {
	const yaml = `
workflow: blocked-resume
start: always-fails
steps:
  - id: always-fails
    kind: deterministic
    run: "true"
    postcondition: "false"
    attempts: 1
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
		t.Fatalf("got %+v, want Terminal{blocked} (unrouted failure)", instr)
	}

	instr, err = e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	term, ok = instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want it to block again immediately (the step still always fails)", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawIntervention bool
	var interventionAttempt int
	for _, ev := range events {
		if ev.Kind == journal.KindResume && ev.Intervention {
			sawIntervention = true
			interventionAttempt = ev.Attempt
		}
	}
	if !sawIntervention {
		t.Fatal("no RESUME{intervention:true} event appended")
	}
	if interventionAttempt != 1 {
		t.Errorf("RESUME.Attempt = %d, want 1 (the counter resets)", interventionAttempt)
	}
}

// TestResume_RefusesDigestMismatch: resuming after the workflow file
// changed is refused (DESIGN.md §4 step 2).
func TestResume_RefusesDigestMismatch(t *testing.T) {
	const yaml = `
workflow: digest-check
start: a
steps:
  - id: a
    kind: deterministic
    run: "true"
    postcondition: "false"
    attempts: 1
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Mutate the in-memory workflow to simulate an edited file.
	e.Workflow.Description = "changed after the run started"

	if _, err := e.Resume("run1", false); err == nil {
		t.Fatal("Resume after a workflow change: want an error, got nil")
	}
}

// TestCaps_MaxStepsBackstop: the workflow-level max_steps: backstop caps
// total step entries across the whole run, independent of any single
// step's max_visits:.
func TestCaps_MaxStepsBackstop(t *testing.T) {
	const yaml = `
workflow: max-steps
start: a
max_steps: 4
steps:
  - id: a
    kind: deterministic
    run: "true"
    max_visits: 3
    next: b
  - id: b
    kind: deterministic
    run: "true"
    max_visits: 3
    next: a
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
	var enters int
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter {
			enters++
		}
	}
	if enters != 4 {
		t.Errorf("total STEP_ENTER = %d, want exactly max_steps (4)", enters)
	}
}
