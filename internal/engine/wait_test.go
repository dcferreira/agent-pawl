package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// waitWorkflow is a deterministic step followed by a wait step: pawl run
// must execute the deterministic body itself and then park at the wait,
// returning a Wait instruction rather than executing poll: or erroring
// (DESIGN.md §2, §3).
const waitWorkflow = `
workflow: wait-sample
start: kick_off
state:
  build_status: {type: string, default: ""}
steps:
  - id: kick_off
    kind: deterministic
    run: touch kicked
    next: wait_for_build
  - id: wait_for_build
    kind: wait
    poll: touch polled
    every: 5s
    timeout: 5m
    emits: pairs
    writes: {build_status: {type: string}}
    outcomes:
      PASSED: done
      FAILED: blocked
      timeout: blocked
terminal:
  done:    {status: ok,      message: "built"}
  blocked: {status: blocked, message: "paused: ${blocked_reason}"}
`

// TestStart_ParksAtWaitStep is the core of this phase: the run loop reaches
// a kind: wait step and parks there, handing back a Wait instruction naming
// the run and the step (DESIGN.md §2's WAIT line).
func TestStart_ParksAtWaitStep(t *testing.T) {
	e := newTestEngine(t, waitWorkflow)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	wait, ok := instr.(Wait)
	if !ok {
		t.Fatalf("Start returned %T, want Wait", instr)
	}
	if wait.RunID != "run1" || wait.Step != "wait_for_build" {
		t.Errorf("Wait = %+v, want run1/wait_for_build", wait)
	}
	if wait.Every != "5s" || wait.Timeout != "5m" {
		t.Errorf("Wait every/timeout = %q/%q, want 5s/5m", wait.Every, wait.Timeout)
	}

	// The deterministic step before it really ran; the wait step's poll:
	// did not (DESIGN.md §3: pawl run prints WAIT, and the polling loop is
	// pawl poll's job — pawl run never invokes poll:, not even once).
	if _, err := os.Stat(filepath.Join(e.Root, "kicked")); err != nil {
		t.Errorf("deterministic step did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.Root, "polled")); err == nil {
		t.Error("pawl run executed the wait step's poll: command; it must not")
	}
}

// TestStart_WaitJournalsParkedState checks the run directory records where
// the run is parked: a STEP_ENTER for the wait step with no TRANSITION after
// it, so Replay's cursor sits on the wait step and the run stays live.
func TestStart_WaitJournalsParkedState(t *testing.T) {
	e := newTestEngine(t, waitWorkflow)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Kind != journal.KindStepEnter || last.Step != "wait_for_build" {
		t.Fatalf("last event = %s/%s, want STEP_ENTER/wait_for_build", last.Kind, last.Step)
	}
	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Terminal() {
		t.Error("run is terminal; a parked wait run must stay live")
	}
	if rs.Cursor.Step != "wait_for_build" || rs.Cursor.Attempt != 1 {
		t.Errorf("cursor = %+v, want wait_for_build/1", rs.Cursor)
	}
	if rs.Visits["wait_for_build"] != 1 {
		t.Errorf("visits[wait_for_build] = %d, want 1", rs.Visits["wait_for_build"])
	}
}

// TestSubmit_RefusedForWaitStep pins DESIGN.md §3's explicit rule: "The
// model never runs pawl submit for a wait result." The refusal must name
// pawl poll, so a session that tried anyway learns what to run instead.
func TestSubmit_RefusedForWaitStep(t *testing.T) {
	e := newTestEngine(t, waitWorkflow)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err := e.Submit("run1", "wait_for_build", 1, json.RawMessage(`{"build_status":"PASSED"}`))
	if err == nil {
		t.Fatal("Submit against a wait step succeeded; it must be refused")
	}
	if !strings.Contains(err.Error(), "pawl poll") || !strings.Contains(err.Error(), "wait") {
		t.Errorf("refusal %q does not name pawl poll and the wait kind", err.Error())
	}
}

// TestResume_ReprintsWaitIdempotently checks DESIGN.md §3's "on resume the
// model simply runs pawl poll again": a second pawl run on a wait-parked run
// re-prints the same WAIT, and — because the engine did no work while parked
// — does not burn another max_visits: entry on the step.
func TestResume_ReprintsWaitIdempotently(t *testing.T) {
	e := newTestEngine(t, waitWorkflow)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	wait, ok := instr.(Wait)
	if !ok {
		t.Fatalf("Resume returned %T, want Wait", instr)
	}
	if wait.RunID != "run1" || wait.Step != "wait_for_build" {
		t.Errorf("Wait = %+v, want run1/wait_for_build", wait)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Visits["wait_for_build"] != 1 {
		t.Errorf("visits[wait_for_build] = %d after a re-run, want 1: re-parking is an idempotent re-print, not a new visit", rs.Visits["wait_for_build"])
	}
	if rs.Cursor.Step != "wait_for_build" {
		t.Errorf("cursor = %+v, want wait_for_build", rs.Cursor)
	}
	if _, err := os.Stat(filepath.Join(e.Root, "polled")); err == nil {
		t.Error("resume executed the wait step's poll: command; it must not")
	}
}
