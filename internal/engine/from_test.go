package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// fromGateWorkflow blocks at gate until a file named ok exists in the
// working copy; trace records every body run, in order.
const fromGateWorkflow = `
workflow: from-gate
start: prep
steps:
  - id: prep
    kind: deterministic
    run: "echo prep >> trace"
    next: gate
  - id: gate
    kind: deterministic
    run: "echo gate >> trace"
    postcondition: "test -f ok"
    attempts: 1
    next: tail
  - id: tail
    kind: deterministic
    run: "echo tail >> trace"
    next: done
terminal: {done: {status: ok}}
`

func fromStartBlocked(t *testing.T, yaml string) (*Engine, string) {
	t.Helper()
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("got %T %+v, want Terminal{blocked}", instr, instr)
	}
	return e, journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
}

func readTrace(t *testing.T, e *Engine) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.Root, "trace"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), ",")
}

func mustEvents(t *testing.T, dir string) []journal.Event {
	t.Helper()
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// TestResumeFrom_BlockedToEarlierStep: a blocked run moved to an earlier
// visited step re-runs from there with state kept, the target's visit
// counted once more and no counter reset; the move is journaled as exactly
// RESUME{intervention} + TRANSITION{via_from}.
func TestResumeFrom_BlockedToEarlierStep(t *testing.T) {
	e, dir := fromStartBlocked(t, fromGateWorkflow)
	before := len(mustEvents(t, dir))

	instr, err := e.ResumeFrom("run1", "prep", false)
	if err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("got %T %+v, want it to block at gate again (ok still missing)", instr, instr)
	}
	if got := readTrace(t, e); got != "prep,gate,prep,gate" {
		t.Errorf("trace = %q, want prep,gate,prep,gate", got)
	}

	events := mustEvents(t, dir)
	res, tr := events[before], events[before+1]
	if res.Kind != journal.KindResume || !res.Intervention || res.Step != "gate" || res.Attempt != 1 {
		t.Errorf("first move event = %+v, want RESUME{step gate, attempt 1, intervention}", res)
	}
	if tr.Kind != journal.KindTransition || !tr.ViaFrom || tr.Step != "gate" || tr.Target != "prep" || tr.Outcome != "" || tr.ViaCatch || tr.Group != "" {
		t.Errorf("second move event = %+v, want TRANSITION{gate -> prep, via_from, no outcome}", tr)
	}
	enter := events[before+2]
	if enter.Kind != journal.KindStepEnter || enter.Step != "prep" || enter.Attempt != 1 || enter.Retry {
		t.Errorf("event after the move = %+v, want a fresh STEP_ENTER of prep at attempt 1", enter)
	}

	rs := mustReplay(t, dir)
	if rs.Visits["prep"] != 2 || rs.Visits["gate"] != 2 {
		t.Errorf("visits = %v, want prep 2, gate 2 (the move counts the target's entry once)", rs.Visits)
	}

	// Fixing the world and moving to the blocked step itself finishes the run.
	if err := os.WriteFile(filepath.Join(e.Root, "ok"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	instr, err = e.ResumeFrom("run1", "gate", false)
	if err != nil {
		t.Fatalf("ResumeFrom gate: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %T %+v, want Terminal{ok}", instr, instr)
	}
	if got := readTrace(t, e); got != "prep,gate,prep,gate,gate,tail" {
		t.Errorf("trace = %q, want prep,gate,prep,gate,gate,tail", got)
	}
}

// TestResumeFrom_RefusesIllegalTargets: an unknown step, a terminal, and a
// top-level step the run has never entered are refused with ErrFromTarget,
// naming the legal set, and nothing is journaled.
func TestResumeFrom_RefusesIllegalTargets(t *testing.T) {
	e, dir := fromStartBlocked(t, fromGateWorkflow)
	before := len(mustEvents(t, dir))
	for _, tc := range []struct{ target, want string }{
		{"nope", `has no step "nope"`},
		{"done", "is a terminal"},
		{"blocked", "is a terminal"},
		{"tail", `step "tail" has not been entered`},
	} {
		_, err := e.ResumeFrom("run1", tc.target, false)
		if !errors.Is(err, ErrFromTarget) {
			t.Errorf("--from %s: err = %v, want ErrFromTarget", tc.target, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "legal targets for this run: prep, gate") {
			t.Errorf("--from %s: err = %q, want it to contain %q and the legal set", tc.target, err, tc.want)
		}
	}
	if got := len(mustEvents(t, dir)); got != before {
		t.Errorf("events = %d after refusals, want %d (a refusal journals nothing)", got, before)
	}
}

// TestResumeFrom_RefusesBranchTarget: a branch of a parallel step is never
// a legal target, even though it is declared under steps:.
func TestResumeFrom_RefusesBranchTarget(t *testing.T) {
	e := newTestEngine(t, fromParallelWorkflow)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err := e.ResumeFrom("run1", "b1", false)
	if !errors.Is(err, ErrFromTarget) || !strings.Contains(err.Error(), `belongs to parallel step "p"`) {
		t.Fatalf("err = %v, want ErrFromTarget naming the owning parallel step", err)
	}
}

// TestResumeFrom_RefusesTerminalRun: a finished run cannot be moved.
func TestResumeFrom_RefusesTerminalRun(t *testing.T) {
	e := newTestEngine(t, fromGateWorkflow)
	if err := os.WriteFile(filepath.Join(e.Root, "ok"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}", instr)
	}
	if _, err := e.ResumeFrom("run1", "prep", false); !errors.Is(err, ErrAlreadyTerminal) {
		t.Fatalf("err = %v, want ErrAlreadyTerminal", err)
	}
}

// TestResumeFrom_DoesNotResetVisits: a target that has used up its
// max_visits: re-blocks on the cap instead of being given a fresh budget.
func TestResumeFrom_DoesNotResetVisits(t *testing.T) {
	yaml := strings.Replace(fromGateWorkflow, "    run: \"echo prep >> trace\"\n", "    run: \"echo prep >> trace\"\n    max_visits: 1\n", 1)
	e, dir := fromStartBlocked(t, yaml)

	instr, err := e.ResumeFrom("run1", "prep", false)
	if err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("got %T %+v, want Terminal{blocked} on prep's max_visits", instr, instr)
	}
	if got := readTrace(t, e); got != "prep,gate" {
		t.Errorf("trace = %q, want prep,gate (prep must not run past its cap)", got)
	}
	if rs := mustReplay(t, dir); rs.Visits["prep"] != 1 {
		t.Errorf("visits[prep] = %d, want 1", rs.Visits["prep"])
	}
}

// TestResumeFrom_InvariantStillChecked: the move itself checks no
// invariant, but the target's own transition does, so an invariant that
// still fails re-blocks the run there.
func TestResumeFrom_InvariantStillChecked(t *testing.T) {
	const yaml = `
workflow: from-invariant
start: a
invariants:
  - id: not-broken
    check: "test ! -f broken"
    message: "broken exists"
steps:
  - id: a
    kind: deterministic
    run: "true"
    next: b
  - id: b
    kind: deterministic
    run: "touch broken"
    next: done
terminal: {done: {status: ok}}
`
	e, dir := fromStartBlocked(t, yaml)
	instr, err := e.ResumeFrom("run1", "a", false)
	if err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("got %T %+v, want Terminal{blocked} (invariant still fails)", instr, instr)
	}
	rs := mustReplay(t, dir)
	if rs.Cursor.Step != "a" {
		t.Errorf("blocked at %q, want a (its transition re-checks the invariant)", rs.Cursor.Step)
	}
}

// fromHumanWaitWorkflow parks at a human step, then (on "go") at a wait
// step or (on "work") at an agentic step.
const fromHumanWaitWorkflow = `
workflow: from-human
start: ask
state:
  choice: {type: string, default: ""}
  build_status: {type: string, default: ""}
  r: {type: string, default: ""}
steps:
  - id: ask
    kind: human
    question: "Which?"
    options: [go, work]
    timeout: 24h
    writes: [choice]
    outcomes:
      go: w
      work: job
      timeout: blocked
  - id: w
    kind: wait
    poll: "echo build_status=PASSED"
    every: 5s
    timeout: 5m
    emits: pairs
    writes: {build_status: {type: string}}
    outcomes:
      PASSED: done
      timeout: blocked
  - id: job
    kind: agentic
    description: "do it"
    writes: {r: {type: string}}
    postcondition: {all_set: [r]}
    next: done
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`

func countKind(events []journal.Event, k journal.Kind, step string) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == k && ev.Step == step {
			n++
		}
	}
	return n
}

// TestResumeFrom_ParkedHumanReasks: --from the current (parked) human step
// asks again, as a fresh visit with its own HUMAN_ASKED.
func TestResumeFrom_ParkedHumanReasks(t *testing.T) {
	e := newTestEngine(t, fromHumanWaitWorkflow)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	instr, err := e.ResumeFrom("run1", "ask", false)
	if err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	if a, ok := instr.(Ask); !ok || a.Step != "ask" {
		t.Fatalf("got %T %+v, want Ask at ask", instr, instr)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	if n := countKind(mustEvents(t, dir), journal.KindHumanAsked, "ask"); n != 2 {
		t.Errorf("HUMAN_ASKED for ask = %d, want 2", n)
	}
	if rs := mustReplay(t, dir); rs.Visits["ask"] != 2 {
		t.Errorf("visits[ask] = %d, want 2", rs.Visits["ask"])
	}
}

// TestResumeFrom_LateSubmitRefused: after moving off an agentic step that
// was awaiting its submit, that submit is refused (exit 4 class).
func TestResumeFrom_LateSubmitRefused(t *testing.T) {
	e := newTestEngine(t, fromHumanWaitWorkflow)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a := instr.(Ask)
	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["work"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	d, ok := instr.(Dispatch)
	if !ok || d.Step != "job" {
		t.Fatalf("got %T %+v, want Dispatch job", instr, instr)
	}
	if _, err := e.ResumeFrom("run1", "ask", false); err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	if _, err := e.Submit("run1", "job", d.Attempt, []byte(`{"r":"x"}`)); !errors.Is(err, ErrRefused) {
		t.Fatalf("late Submit err = %v, want ErrRefused", err)
	}
}

// TestResumeFrom_LatePollRefused: after moving off a parked wait step, a
// poll of it is refused (ErrPollNotCurrent).
func TestResumeFrom_LatePollRefused(t *testing.T) {
	e := newTestEngine(t, fromHumanWaitWorkflow)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a := instr.(Ask)
	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["go"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	if w, ok := instr.(Wait); !ok || w.Step != "w" {
		t.Fatalf("got %T %+v, want Wait at w", instr, instr)
	}
	if _, err := e.ResumeFrom("run1", "ask", false); err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	if _, err := e.Poll("run1", "w", nil); !errors.Is(err, ErrPollNotCurrent) {
		t.Fatalf("late Poll err = %v, want ErrPollNotCurrent", err)
	}
}

// TestResumeFrom_WaitCursorFreshVisit: --from the current wait step is a
// fresh visit (a new STEP_ENTER, so a new deadline).
func TestResumeFrom_WaitCursorFreshVisit(t *testing.T) {
	e := newTestEngine(t, fromHumanWaitWorkflow)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a := instr.(Ask)
	if _, err := e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["go"]}`)); err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	instr, err = e.ResumeFrom("run1", "w", false)
	if err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	if w, ok := instr.(Wait); !ok || w.Step != "w" {
		t.Fatalf("got %T %+v, want Wait at w", instr, instr)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	if n := countKind(mustEvents(t, dir), journal.KindStepEnter, "w"); n != 2 {
		t.Errorf("STEP_ENTER for w = %d, want 2", n)
	}
}

// TestResumeFrom_RunningPollerStopsOnReentry: a poller already running on a
// wait step stops (ErrPollNotCurrent) once --from re-enters that step, rather
// than carrying its old visit's deadline into the new visit; nothing it saw
// is journaled against the new visit.
func TestResumeFrom_RunningPollerStopsOnReentry(t *testing.T) {
	yaml := strings.Replace(fromHumanWaitWorkflow, `poll: "echo build_status=PASSED"`, `poll: "echo not-yet"`, 1)
	e := newTestEngine(t, yaml)
	e.Sleep = func(time.Duration) {}
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a := instr.(Ask)
	if _, err := e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["go"]}`)); err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")

	moved := false
	_, err = e.Poll("run1", "w", func(it PollIteration) {
		if moved {
			return
		}
		moved = true
		if _, err := e.ResumeFrom("run1", "w", false); err != nil {
			t.Errorf("ResumeFrom during poll: %v", err)
		}
	})
	if !moved {
		t.Fatal("the poll loop never ran an iteration")
	}
	if !errors.Is(err, ErrPollNotCurrent) || !strings.Contains(err.Error(), "entered again") {
		t.Fatalf("old poller err = %v, want ErrPollNotCurrent naming the re-entry", err)
	}
	rs := mustReplay(t, dir)
	if rs.Cursor.Step != "w" || rs.Status() != "running" || rs.Visits["w"] != 2 {
		t.Errorf("cursor %+v status %s visits %v, want parked at w on its second visit", rs.Cursor, rs.Status(), rs.Visits)
	}
}

const fromParallelWorkflow = `
workflow: from-parallel
start: pre
state:
  r1: {type: string, default: ""}
  r2: {type: string, default: ""}
steps:
  - id: pre
    kind: deterministic
    run: "true"
    next: p
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: done
  - id: b1
    kind: agentic
    description: "do b1"
    writes: {r1: {type: string}}
    postcondition: {all_set: [r1]}
  - id: b2
    kind: agentic
    description: "do b2"
    writes: {r2: {type: string}}
    postcondition: {all_set: [r2]}
terminal: {done: {status: ok}}
`

// TestResumeFrom_RefusesParallelWithPendingBranches: moving off a parallel
// step with branches still pending is refused (ErrRefused) and journals
// nothing; once every branch has reported, the step is no longer pending.
func TestResumeFrom_RefusesParallelWithPendingBranches(t *testing.T) {
	e := newTestEngine(t, fromParallelWorkflow)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	before := len(mustEvents(t, dir))
	_, err := e.ResumeFrom("run1", "pre", false)
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "branch b1, branch b2") {
		t.Fatalf("err = %v, want ErrRefused naming both pending branches", err)
	}
	if _, err := e.Submit("run1", "b1", 1, []byte(`{"r1":"x"}`)); err != nil {
		t.Fatalf("Submit b1: %v", err)
	}
	_, err = e.ResumeFrom("run1", "pre", false)
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "branch b2") || strings.Contains(err.Error(), "branch b1") {
		t.Fatalf("err = %v, want ErrRefused naming only b2", err)
	}
	events := mustEvents(t, dir)
	for _, ev := range events[before:] {
		if ev.ViaFrom || ev.Kind == journal.KindResume {
			t.Errorf("a refused move journaled %+v", ev)
		}
	}
}

// TestResumeFrom_RefusesForeachWithPendingItems: the same for a foreach
// step whose agentic items are still out.
func TestResumeFrom_RefusesForeachWithPendingItems(t *testing.T) {
	e, _, _ := agStart(t, feAgentic(`["a","b"]`, agBody, "", ""))
	_, err := e.ResumeFrom("run1", "produce", false)
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "item 0, item 1") {
		t.Fatalf("err = %v, want ErrRefused naming both pending items", err)
	}
}

// TestResumeFrom_CrashAfterMoveIsFree: a crash right after the move (before
// the target's STEP_ENTER) resumes at the target and counts its visit once.
func TestResumeFrom_CrashAfterMoveIsFree(t *testing.T) {
	e, dir := fromStartBlocked(t, fromGateWorkflow)
	if _, err := e.ResumeFrom("run1", "prep", false); err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	truncateAfterViaFrom(t, dir)
	if rs := mustReplay(t, dir); rs.Cursor.Step != "prep" || rs.Cursor.Attempt != 1 || rs.Status() != "running" {
		t.Fatalf("after crash: cursor %+v status %s, want prep attempt 1 running", rs.Cursor, rs.Status())
	}

	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("got %T %+v, want blocked again at gate", instr, instr)
	}
	if rs := mustReplay(t, dir); rs.Visits["prep"] != 2 || rs.Visits["gate"] != 2 {
		t.Errorf("visits = %v, want prep 2, gate 2", rs.Visits)
	}
}

// truncateAfterViaFrom cuts events.jsonl right after the (last) via_from
// TRANSITION, simulating a crash before the target was entered.
func truncateAfterViaFrom(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "events.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(b, []byte("\n"))
	cut := -1
	for i, l := range lines {
		if bytes.Contains(l, []byte(`"via_from":true`)) {
			cut = i
		}
	}
	if cut < 0 {
		t.Fatal("no via_from record in events.jsonl")
	}
	if err := os.WriteFile(path, bytes.Join(lines[:cut+1], nil), 0o644); err != nil {
		t.Fatal(err)
	}
}

// v100EventKeys is the set of JSON keys journal.Event had at v1.0.0. Frozen
// here on purpose: it is what an older 1.x binary can read.
var v100EventKeys = map[string]bool{
	"kind": true, "run_id": true, "seq": true, "time": true, "step": true, "attempt": true,
	"attempt_key": true, "retry": true, "hard_retry": true, "group": true, "item": true,
	"items": true, "format": true, "args": true, "digest": true, "resumed": true,
	"hook_self_test": true, "intervention": true, "writes": true, "ok": true, "text": true,
	"soft": true, "summary": true, "target": true, "outcome": true, "via_catch": true,
	"status": true, "reason": true, "note": true,
}

// v100Kinds is the set of event kinds journal had at v1.0.0, frozen the same
// way.
var v100Kinds = map[journal.Kind]bool{
	"RUN_START": true, "RESUME": true, "STEP_ENTER": true, "WRITES": true,
	"POSTCONDITION": true, "TRANSITION": true, "HUMAN_ASKED": true, "RUN_END": true,
}

// TestResumeFrom_JournalReadableByOlder1x is the old-binary guarantee for a
// moved run: every record the move writes uses a v1.0.0 event kind and only
// v1.0.0 keys plus via_from, which an older binary's decoder ignores; and
// replaying the journal with via_from stripped (what an older binary sees)
// gives exactly the state this build replays.
func TestResumeFrom_JournalReadableByOlder1x(t *testing.T) {
	e, dir := fromStartBlocked(t, fromGateWorkflow)
	before := len(mustEvents(t, dir))
	if _, err := e.ResumeFrom("run1", "prep", false); err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	truncateAfterViaFrom(t, dir)

	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != before+2 {
		t.Fatalf("lines = %d, want %d (the move writes two records)", len(lines), before+2)
	}
	var stripped []byte
	for i, l := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatal(err)
		}
		if i >= before {
			if !v100Kinds[journal.Kind(rec["kind"].(string))] {
				t.Errorf("move record %d has kind %v, unknown to v1.0.0", i, rec["kind"])
			}
			for k := range rec {
				if !v100EventKeys[k] && k != "via_from" {
					t.Errorf("move record %d has key %q, unknown to v1.0.0", i, k)
				}
			}
		}
		delete(rec, "via_from")
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		stripped = append(append(stripped, b...), '\n')
	}

	oldDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(oldDir, "events.jsonl"), stripped, 0o644); err != nil {
		t.Fatal(err)
	}
	got := mustReplay(t, oldDir)
	want := mustReplay(t, dir)
	if !reflect.DeepEqual(got.Cursor, want.Cursor) || !reflect.DeepEqual(got.Visits, want.Visits) ||
		!reflect.DeepEqual(got.Attempts, want.Attempts) || got.Status() != want.Status() {
		t.Errorf("replay without via_from = cursor %+v visits %v attempts %v status %s; with = cursor %+v visits %v attempts %v status %s",
			got.Cursor, got.Visits, got.Attempts, got.Status(), want.Cursor, want.Visits, want.Attempts, want.Status())
	}
	if want.Cursor.Step != "prep" || want.Cursor.Attempt != 1 || want.Status() != "running" {
		t.Errorf("cursor %+v status %s, want prep attempt 1 running", want.Cursor, want.Status())
	}
	for ref := range want.Attempts {
		if ref.Step == "gate" {
			t.Errorf("attempts = %v, want gate's budget cleared by the move", want.Attempts)
		}
	}
}
