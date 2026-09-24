package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// TestStart_ThreeDeterministicSteps is the end-to-end test the brief's
// definition of done asks for: a workflow with no agentic steps runs start
// to finish in one call (DESIGN.md §2).
func TestStart_ThreeDeterministicSteps(t *testing.T) {
	const yaml = `
workflow: three-step
start: a
state:
  trail: {type: string, default: ""}
steps:
  - id: a
    kind: deterministic
    run: 'echo -n "${trail}a" > trail.txt; cat trail.txt'
    emits: pairs
    next: b
  - id: b
    kind: deterministic
    run: touch marker-b
    next: c
  - id: c
    kind: deterministic
    run: touch marker-c
    next: done
terminal:
  done: {status: ok, message: "finished"}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok {
		t.Fatalf("Start returned %T, want Terminal", instr)
	}
	if term.Status != "ok" || term.StepID != "done" || term.Message != "finished" {
		t.Errorf("got %+v", term)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var enters []string
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter {
			enters = append(enters, ev.Step)
		}
	}
	want := []string{"a", "b", "c"}
	if len(enters) != len(want) {
		t.Fatalf("STEP_ENTER order = %v, want %v", enters, want)
	}
	for i := range want {
		if enters[i] != want[i] {
			t.Errorf("STEP_ENTER[%d] = %q, want %q", i, enters[i], want[i])
		}
	}

	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if !rs.Terminal() || rs.EndStatus != "ok" {
		t.Errorf("replayed state not terminal/ok: %+v", rs)
	}
}

// TestStart_MissingRequiredArg checks args: required: refuses to start.
func TestStart_MissingRequiredArg(t *testing.T) {
	const yaml = `
workflow: needs-arg
start: a
args:
  name: {type: string, required: true}
steps:
  - id: a
    kind: deterministic
    run: "true"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	if _, err := e.Start("run1", nil); err == nil {
		t.Fatal("Start with missing required arg: want error, got nil")
	}
}

// TestSubmit_RefusesWrongAttempt is the brief's other required test: Submit
// must refuse any (run, step, attempt) triple other than the one the
// journal says the engine is waiting on, naming what it is waiting on.
func TestSubmit_RefusesWrongAttempt(t *testing.T) {
	const yaml = `
workflow: one-agentic
start: work
state:
  result: {type: string, default: ""}
steps:
  - id: work
    kind: agentic
    description: "do the thing"
    writes: {result: {type: string}}
    postcondition: "true"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	dispatch, ok := instr.(Dispatch)
	if !ok {
		t.Fatalf("Start returned %T, want Dispatch", instr)
	}
	if dispatch.Attempt != 1 {
		t.Fatalf("dispatch attempt = %d, want 1", dispatch.Attempt)
	}

	_, err = e.Submit("run1", "work", 2, []byte(`{"result":"x"}`))
	if err == nil {
		t.Fatal("Submit with wrong attempt: want error, got nil")
	}
	t.Logf("got expected error: %v", err)

	_, err = e.Submit("run1", "nope", 1, []byte(`{"result":"x"}`))
	if err == nil {
		t.Fatal("Submit with wrong step: want error, got nil")
	}

	// The correct triple still works after the wrong ones were refused.
	instr, err = e.Submit("run1", "work", 1, []byte(`{"result":"x"}`))
	if err != nil {
		t.Fatalf("Submit with the correct triple: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{Status:ok}", instr)
	}
}

// TestStart_RecordsRealEnforcementMode: RUN_START's hook_self_test field must record what checkEnforcement
// actually decided (via Engine.Enforcement), never a hardcoded placeholder.
// Left unset (as newTestEngine leaves it — this package's tests never go
// through cmdRun's checkEnforcement), it records the neutral "unknown"
// rather than claiming a mode nothing checked.
func TestStart_RecordsRealEnforcementMode(t *testing.T) {
	const yaml = `
workflow: enforcement-record
start: a
steps:
  - id: a
    kind: deterministic
    run: 'true'
    next: done
terminal:
  done: {status: ok, message: "finished"}
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
	if events[0].Kind != journal.KindRunStart || events[0].HookSelfTest != "unknown" {
		t.Fatalf("got RUN_START.hook_self_test = %q, want %q", events[0].HookSelfTest, "unknown")
	}

	e2 := newTestEngine(t, yaml)
	e2.Enforcement = "on (PreToolUse heartbeat)"
	if _, err := e2.Start("run2", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	dir2 := journal.RunDir(e2.Root, e2.Workflow.Workflow, "run2")
	events2, err := journal.ReadEvents(dir2)
	if err != nil {
		t.Fatal(err)
	}
	if events2[0].HookSelfTest != "on (PreToolUse heartbeat)" {
		t.Fatalf("got RUN_START.hook_self_test = %q, want %q", events2[0].HookSelfTest, "on (PreToolUse heartbeat)")
	}
}

// TestStart_OnRunStartFiresBeforePrefix: OnRunStart is called once, right
// after RUN_START is journaled and before the first step runs — so pawl run
// can stamp driver.json and link live/ before a long deterministic prefix
// that might be killed partway through.
func TestStart_OnRunStartFiresBeforePrefix(t *testing.T) {
	const yaml = `
workflow: onstart
start: a
steps:
  - id: a
    kind: deterministic
    run: touch marker-a
    next: done
terminal:
  done: {status: ok}
`
	e := newTestEngine(t, yaml)
	calls := 0
	e.OnRunStart = func(dir, runID string) {
		calls++
		if runID != "run1" || dir != journal.RunDir(e.Root, "onstart", "run1") {
			t.Errorf("OnRunStart(%q, %q)", dir, runID)
		}
		events, err := journal.ReadEvents(dir)
		if err != nil || len(events) != 1 || events[0].Kind != journal.KindRunStart {
			t.Errorf("at OnRunStart want exactly RUN_START journaled, got %v (err %v)", events, err)
		}
		if _, err := os.Stat(filepath.Join(e.Root, "marker-a")); !os.IsNotExist(err) {
			t.Errorf("step a already ran before OnRunStart (err %v)", err)
		}
	}
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnRunStart called %d times, want 1", calls)
	}
}
