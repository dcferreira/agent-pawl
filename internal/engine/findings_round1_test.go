package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dcferreira/agentic-workflow-fsm/internal/emit"
	"github.com/dcferreira/agentic-workflow-fsm/internal/journal"
	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// TestCaps_C1_RetriesDoNotBypassOrInflateMaxVisits is the probe-P1
// regression: max_visits: 2, attempts: 3, a postcondition that always fails.
// Before C1, every attempt-retry's STEP_ENTER counted as a visit, so a
// single visit's three attempts alone inflated Visits to 3 with no
// "exhausted" ever produced. After the fix, each visit still gets its full
// 3 attempts (attempts: is untouched), but only two *visits* ever happen
// before max_visits: binds and "exhausted" is produced.
func TestCaps_C1_RetriesDoNotBypassOrInflateMaxVisits(t *testing.T) {
	const yaml = `
workflow: c1-max-visits
start: check
steps:
  - id: check
    kind: deterministic
    run: "true"
    postcondition: "false"
    attempts: 3
    max_visits: 2
    next: done
    catch: [{on: failure, next: check}]
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} once max_visits: binds", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var totalEnters, retryEnters int
	var sawExhausted bool
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter && ev.Step == "check" {
			totalEnters++
			if ev.Retry {
				retryEnters++
			}
		}
		if ev.Kind == journal.KindTransition && ev.Outcome == "exhausted" {
			sawExhausted = true
		}
	}
	if !sawExhausted {
		t.Error("no TRANSITION{outcome:exhausted} recorded — max_visits: never bound")
	}
	// Visit 1 gets its full 3-attempt budget (2 retries). The postcondition
	// always fails with the same (empty) text, so per §B.4 "not scoped to
	// the incoming edge", visit 2's very first try continues that same
	// budget rather than starting a fresh one — it is already over
	// attempts: 3 the moment it fails once, so it contributes only 1
	// STEP_ENTER, not 3. Total: 3 + 1 = 4, with 2 retries (both in visit 1).
	if totalEnters != 4 {
		t.Errorf("total STEP_ENTER(check) = %d, want 4 (visit 1: 3 attempts; visit 2: 1, already over budget)", totalEnters)
	}
	if retryEnters != 2 {
		t.Errorf("retry STEP_ENTER(check) = %d, want 2", retryEnters)
	}

	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Visits["check"] != 2 {
		t.Errorf("Visits[check] = %d, want 2 (real visits only, not attempts)", rs.Visits["check"])
	}
}

// pidIsAlive reports whether pid names a live process, via a signal-0
// probe — mirroring internal/journal's own liveness check, duplicated here
// rather than imported since it is unexported there.
func pidIsAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// TestExec_C2_BackgroundedGrandchildKilledByCeiling is probe P3's round-2
// regression: an elapsed-time bound cannot distinguish "the grandchild was
// actually killed" from "WaitDelay gave up waiting for it" — the round-1
// version of this test passed for the wrong reason (satisfied by WaitDelay
// expiring, not by the process-group kill working). This asserts the thing
// that actually matters: the backgrounded grandchild's pid is gone, not
// just that Start returned inside some elapsed bound.
func TestExec_C2_BackgroundedGrandchildKilledByCeiling(t *testing.T) {
	const yaml = `
workflow: c2-backgrounded
start: a
steps:
  - id: a
    kind: deterministic
    run: "sleep 43 & echo $! > child.pid; echo started"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	e.Timeout = 300 * time.Millisecond

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} (timeout -> failure -> default blocked)", instr)
	}

	pidBytes, err := os.ReadFile(filepath.Join(e.Root, "child.pid"))
	if err != nil {
		t.Fatalf("reading child.pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parsing child.pid: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for pidIsAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if pidIsAlive(pid) {
		t.Fatalf("pid %d (the backgrounded `sleep 43`) is still alive after Start returned — the process-group kill did not reach it", pid)
	}
}

// TestI1_HardFailureSetsLastError: DESIGN.md §3 says exceeding the ceiling,
// or any other hard failure, is "failure with last_error set" — but neither
// path ever ran a postcondition, so nothing populated it before this fix.
func TestI1_HardFailureSetsLastError(t *testing.T) {
	const yaml = `
workflow: i1-last-error
start: a
steps:
  - id: a
    kind: deterministic
    run: 'echo "boom on stderr" >&2; exit 7'
    next: done
terminal: {done: {status: ok, message: "stopped: ${last_error}"}}
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

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	rs, err := replayDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rs.LastError, "boom on stderr") {
		t.Errorf("LastError = %q, want it to contain the step's stderr", rs.LastError)
	}
}

// TestI1_LastErrorNotStaleAcrossSteps: a step's own hard failure must not be
// left showing a wholly unrelated, earlier step's error text — the flip
// side of the "never set" half of I1's finding.
func TestI1_LastErrorNotStaleAcrossSteps(t *testing.T) {
	const yaml = `
workflow: i1-not-stale
start: a
state:
  x: {type: string, default: ""}
steps:
  - id: a
    kind: deterministic
    run: "true"
    postcondition: 'echo "a-specific failure text"; exit 1'
    attempts: 1
    next: b
    catch: [{on: failure, next: b}]
  - id: b
    kind: deterministic
    run: "echo done"
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
		t.Fatalf("got %+v, want Terminal{ok}: b runs cleanly after a's caught failure", instr)
	}
	// b has no postcondition and never fails, so LastError from a's failure
	// legitimately survives to the end (journal.RunState.LastError is
	// whole-run, most-recent — this is documented, existing behaviour, not
	// itself a bug); this test exists to pin that behaviour is unchanged by
	// the I1 fix, not to demand it be cleared.
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	rs, err := replayDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rs.LastError, "a-specific failure text") {
		t.Errorf("LastError = %q, want it to contain a's failure text", rs.LastError)
	}
}

// TestI2_StdoutStderrSurviveTheStep: DESIGN.md §3 requires stdout/stderr
// "captured in full"; before this fix execDeterministic discarded stderr
// entirely and nothing was ever persisted anywhere.
func TestI2_StdoutStderrSurviveTheStep(t *testing.T) {
	const yaml = `
workflow: i2-logs
start: a
steps:
  - id: a
    kind: deterministic
    run: 'echo "to stdout"; echo "to stderr" >&2'
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	stdout, err := os.ReadFile(filepath.Join(dir, "logs", "a.1.stdout"))
	if err != nil {
		t.Fatalf("reading persisted stdout: %v", err)
	}
	if !strings.Contains(string(stdout), "to stdout") {
		t.Errorf("stdout log = %q, want it to contain the step's stdout", stdout)
	}
	stderr, err := os.ReadFile(filepath.Join(dir, "logs", "a.1.stderr"))
	if err != nil {
		t.Fatalf("reading persisted stderr: %v", err)
	}
	if !strings.Contains(string(stderr), "to stderr") {
		t.Errorf("stderr log = %q, want it to contain the step's stderr", stderr)
	}
}

// TestI3_UnintelligibleStdoutIsRoutedNotThrown is probe P4's regression: an
// author-named-outcomes step whose stdout carries no TOKEN used to return a
// raw Go error out of Start, leaving a dangling STEP_ENTER with no
// TRANSITION/RUN_END — a permanently wedged run. It must instead be routed
// (this build routes it as a "failure", per the brief's own instruction to
// branch on errors.Is(err, emit.ErrParse)).
func TestI3_UnintelligibleStdoutIsRoutedNotThrown(t *testing.T) {
	const yaml = `
workflow: i3-unparseable
start: a
steps:
  - id: a
    kind: deterministic
    run: "echo not-a-routed-token"
    outcomes: {GOOD: done, BAD: done}
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start returned a raw Go error instead of routing: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} (unrouted failure)", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawTransition, sawRunEnd bool
	for _, ev := range events {
		if ev.Kind == journal.KindTransition {
			sawTransition = true
		}
		if ev.Kind == journal.KindRunEnd {
			sawRunEnd = true
		}
	}
	if !sawTransition || !sawRunEnd {
		t.Errorf("journal is missing a TRANSITION and/or RUN_END — the run is still wedged (transition=%v, run_end=%v)", sawTransition, sawRunEnd)
	}

	// BLOCKED is paused, not terminal (design/format-spec.md §B.12), so
	// Resume must cleanly re-run the step and report blocked again — not
	// die on the same raw Go error a second time, the original P4 symptom
	// ("Resume re-enters, dies identically").
	instr, err = e.Resume("run1", false)
	if err != nil {
		if errors.Is(err, emit.ErrParse) {
			t.Fatalf("Resume died on the same unintelligible stdout again: %v", err)
		}
		t.Fatalf("Resume: %v", err)
	}
	term, ok = instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("Resume: got %+v, want Terminal{blocked} again (the authoring bug is still there — a human must fix the workflow)", instr)
	}
}

// TestI5_PreviousFailureSurvivesCrossVisitReentry is probes P6/P13's
// regression: an agentic step's attempt budget correctly continues across a
// re-entry by a different edge (nextTryNumber was already right), but the
// redispatched DISPATCH's PreviousFailure was empty because the bootstrap
// STEP_ENTER for that new visit is correctly attempt 1/key "" until it
// fails again — the previous *reporting*, not the counter, was wrong.
func TestI5_PreviousFailureSurvivesCrossVisitReentry(t *testing.T) {
	const yaml = `
workflow: i5-cross-visit
start: work
state:
  findings: {type: json, default: []}
steps:
  - id: work
    kind: agentic
    description: "fix ${findings}"
    writes: {findings: {type: json}}
    postcondition: {command: "echo 'still not fixed'; exit 1"}
    attempts: 2
    next: done
    catch: [{on: failure, next: retry_router}]
  - id: retry_router
    kind: deterministic
    run: "true"
    next: work
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	d, ok := instr.(Dispatch)
	if !ok || d.Attempt != 1 {
		t.Fatalf("got %+v, want Dispatch attempt 1", instr)
	}

	// Attempt 1 of visit 1 fails (postcondition "false" always fails, same
	// text every time); attempts: 2 means it retries once more in the same
	// visit.
	instr, err = e.Submit("run1", "work", 1, []byte(`{"findings": [1]}`))
	if err != nil {
		t.Fatalf("Submit attempt 1: %v", err)
	}
	d, ok = instr.(Dispatch)
	if !ok || d.Attempt != 2 {
		t.Fatalf("got %+v, want Dispatch attempt 2 (still within visit 1's budget)", instr)
	}

	// Attempt 2 also fails: visit 1's budget (attempts: 2) is now exhausted,
	// so this routes via catch to retry_router and back into "work" for a
	// second VISIT — a fresh edge arrival, bootstrapping attempt 1/key "".
	instr, err = e.Submit("run1", "work", 2, []byte(`{"findings": [1]}`))
	if err != nil {
		t.Fatalf("Submit attempt 2: %v", err)
	}
	d, ok = instr.(Dispatch)
	if !ok {
		t.Fatalf("got %+v, want a fresh Dispatch for visit 2", instr)
	}
	if d.Attempt != 1 {
		t.Fatalf("visit 2's bootstrap Dispatch.Attempt = %d, want 1 (the counter itself is unaffected by this fix)", d.Attempt)
	}
	if d.PreviousFailure == "" {
		t.Error("PreviousFailure is empty on visit 2's bootstrap dispatch — the previous, still-live failure text was lost across the cross-visit re-entry (finding I5)")
	}
}

// TestN1_LogPathEscapeIsSanitized is finding N1's engine-level regression,
// introduced by I2's own fix: a step id containing path-traversal
// characters must not let its log file land outside the run directory.
// spec.Validate now rejects such an id at author time (see
// internal/spec's "N1: step id is not identifier-like" golden-message
// test), so this builds the Workflow value directly, as the fail-closed
// tests do, to prove the engine's own defence holds even for a Workflow
// that reached it some other way.
func TestN1_LogPathEscapeIsSanitized(t *testing.T) {
	w := &spec.Workflow{
		Workflow: "n1-log-escape",
		Start:    "../../escaped",
		MaxSteps: 200,
		Steps: []spec.Step{{
			ID:        "../../escaped",
			Kind:      "deterministic",
			Run:       "echo hi",
			MaxVisits: 10,
			Attempts:  1,
			Next:      "done",
		}},
		Terminal: map[string]spec.Terminal{"done": {Status: "ok"}},
	}
	stateDir := t.TempDir()
	t.Setenv(journal.EnvStateDir, stateDir)
	root := t.TempDir()
	e := &Engine{Workflow: w, Root: root, Timeout: 5 * time.Second}

	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	logDir := filepath.Join(dir, "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("reading logs dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no log files were written at all")
	}
	for _, entry := range entries {
		full := filepath.Join(logDir, entry.Name())
		if !isWithinDir(logDir, full) {
			t.Errorf("log file %q escaped the run directory", full)
		}
		if strings.ContainsAny(entry.Name(), "/.") && !strings.HasSuffix(entry.Name(), ".stdout") && !strings.HasSuffix(entry.Name(), ".stderr") {
			t.Errorf("log file name %q looks unsanitised", entry.Name())
		}
	}
	// Nothing must have been written above the run's own state tree.
	if _, err := os.Stat(filepath.Join(stateDir, "escaped.1.stdout")); !os.IsNotExist(err) {
		t.Errorf("a log file escaped into %q", stateDir)
	}
}

// TestN2_MissingContextFileIsRoutedNotThrown is finding N2: A1's own fix
// introduced a new wedge — a missing context: file used to hard-error out of
// Start with a raw Go error, leaving a dangling STEP_ENTER and no
// TRANSITION/RUN_END, exactly the I3 wedge class at a new site. It must be
// routed as a failure instead.
func TestN2_MissingContextFileIsRoutedNotThrown(t *testing.T) {
	const yaml = `
workflow: n2-missing-context
start: work
state:
  result: {type: string, default: ""}
steps:
  - id: work
    kind: agentic
    description: "d"
    context: [does-not-exist.md]
    writes: {result: {type: string}}
    postcondition: "true"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start returned a raw Go error instead of routing: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} (unrouted failure)", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawTransition, sawRunEnd bool
	for _, ev := range events {
		if ev.Kind == journal.KindTransition {
			sawTransition = true
		}
		if ev.Kind == journal.KindRunEnd {
			sawRunEnd = true
		}
	}
	if !sawTransition || !sawRunEnd {
		t.Errorf("journal is missing a TRANSITION and/or RUN_END — the run is wedged (transition=%v, run_end=%v)", sawTransition, sawRunEnd)
	}

	rs, err := replayDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rs.LastError, "does-not-exist.md") {
		t.Errorf("LastError = %q, want it to name the missing file", rs.LastError)
	}

	// Resuming must not die identically (the original wedge symptom).
	if _, err := e.Resume("run1", false); err != nil {
		t.Fatalf("Resume: %v", err)
	}
}

// TestI4_CatchOnlyOutcomeRoutesEndToEnd is finding I4's round-2 requirement:
// a resolveTarget unit test alone was not evidence the scenario worked,
// since emit.Parse rejected the token before resolveTarget was ever reached.
// This runs the literal scenario from the finding end to end: a
// deterministic step with outcomes: {good: done} and catch: [{on: bad,
// next: fallback}], whose stdout carries "bad" — a token named only by
// catch:, with no outcomes: entry of its own.
func TestI4_CatchOnlyOutcomeRoutesEndToEnd(t *testing.T) {
	const yaml = `
workflow: i4-catch-only
start: a
steps:
  - id: a
    kind: deterministic
    run: "echo bad"
    emits: pairs
    outcomes: {good: done}
    catch: [{on: bad, next: fallback}]
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
		t.Fatalf("got %+v, want Terminal{ok} via the catch-only \"bad\" route to fallback", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawBadTransition bool
	for _, ev := range events {
		if ev.Kind == journal.KindTransition && ev.Step == "a" {
			if ev.Outcome != "bad" || ev.Target != "fallback" {
				t.Errorf("TRANSITION for step a = %+v, want outcome \"bad\" -> fallback", ev)
			}
			sawBadTransition = true
		}
	}
	if !sawBadTransition {
		t.Error("no TRANSITION recorded for step a")
	}
}

// TestA1_ContextCmdEntryIsExecuted is finding A1's engine-level regression:
// a "!cmd"-tagged context: entry must have ${key} substituted and then be
// executed, with its captured stdout as the gathered value — not treated as
// a file path (the earlier, rejected fallback would have handed the
// subagent the literal string "echo hi", never running it).
func TestA1_ContextCmdEntryIsExecuted(t *testing.T) {
	const yaml = `
workflow: a1-cmd-context
start: work
state:
  greeting: {type: string, default: "hi"}
  result: {type: string, default: ""}
steps:
  - id: work
    kind: agentic
    description: "look at the context"
    context:
      - !cmd "echo ${greeting}-from-cmd"
      - notes.md
    writes: {result: {type: string}}
    postcondition: "true"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.Workflow.Path), "notes.md"), []byte("file contents here"), 0o644); err != nil {
		t.Fatal(err)
	}

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	d, ok := instr.(Dispatch)
	if !ok {
		t.Fatalf("got %+v, want Dispatch", instr)
	}
	if len(d.Context) != 2 {
		t.Fatalf("got %d context items, want 2", len(d.Context))
	}
	if !strings.Contains(d.Context[0].Value, "hi-from-cmd") {
		t.Errorf("context[0] (!cmd entry) = %+v, want its Value to contain the command's stdout \"hi-from-cmd\"", d.Context[0])
	}
	if !strings.Contains(d.Context[1].Value, "file contents here") {
		t.Errorf("context[1] (plain entry) = %+v, want its Value to be the file's contents", d.Context[1])
	}
}

// TestA1_ContextFileResolvesRelativeToWorkflowFile: a plain context: entry
// is a file path relative to the workflow file's own directory, per
// design/format-spec.md §I ("scripts/ resolve relative to the workflow
// file"), not the working-copy root (which may be a different directory
// entirely in a real repo).
func TestA1_ContextFileResolvesRelativeToWorkflowFile(t *testing.T) {
	const yaml = `
workflow: a1-file-context
start: work
state:
  result: {type: string, default: ""}
steps:
  - id: work
    kind: agentic
    description: "d"
    context: [sibling.txt]
    writes: {result: {type: string}}
    postcondition: "true"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	workflowDir := filepath.Dir(e.Workflow.Path)
	if workflowDir == e.Root {
		t.Fatal("test setup: workflow dir must differ from the working-copy root to prove resolution isn't against Root")
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "sibling.txt"), []byte("sibling content"), 0o644); err != nil {
		t.Fatal(err)
	}

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	d, ok := instr.(Dispatch)
	if !ok || len(d.Context) != 1 {
		t.Fatalf("got %+v, want a single-item Dispatch", instr)
	}
	if d.Context[0].Value != "sibling content" {
		t.Errorf("context[0].Value = %q, want \"sibling content\"", d.Context[0].Value)
	}
}
