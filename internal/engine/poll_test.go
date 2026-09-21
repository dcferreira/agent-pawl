package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// pollWorkflow is the shape the wait-for-build example has: a deterministic
// kick-off, a wait step whose poll: reads a status file, and an agentic step
// only the PASSED route reaches. poll: is deliberately a bare `cat status`
// (no "/" in its first token), so it resolves against the engine's own
// working-copy root — the directory these tests write the status file into.
const pollWorkflow = `
workflow: poll-sample
start: kick_off
state:
  build_status: {type: string, default: ""}
  summary:      {type: string, default: ""}
steps:
  - id: kick_off
    kind: deterministic
    run: "true"
    next: wait_for_build
  - id: wait_for_build
    kind: wait
    poll: cat status
    every: 1s
    timeout: 10s
    emits: pairs
    writes: {build_status: {type: string}}
    outcomes:
      PASSED: summarize_build
      FAILED: blocked
      timeout: blocked
  - id: summarize_build
    kind: agentic
    description: "summarise ${build_status}"
    writes: {summary: {type: string}}
    postcondition: {all_set: [summary]}
    next: done
terminal:
  done:    {status: ok,      message: "built"}
  blocked: {status: blocked, message: "paused: ${blocked_reason}"}
`

// fakeClock replaces the engine's only two clock reads (Now, Sleep) so a
// 10s timeout: and a 1s every: cost a test no wall-clock time at all. It
// starts at the real now, because the timeout clock's origin is the parked
// STEP_ENTER's own journalled (real) timestamp.
type fakeClock struct {
	now     time.Time
	slept   []time.Duration
	onSleep func()
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Now()} }

func (c *fakeClock) install(e *Engine) {
	e.Now = func() time.Time { return c.now }
	e.Sleep = func(d time.Duration) {
		c.slept = append(c.slept, d)
		c.now = c.now.Add(d)
		if c.onSleep != nil {
			c.onSleep()
		}
	}
}

// writeStatus writes the poll script's input file under the engine's root.
func writeStatus(t *testing.T, e *Engine, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.Root, "status"), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func startParked(t *testing.T, e *Engine, runID string) {
	t.Helper()
	instr, err := e.Start(runID, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := instr.(Wait); !ok {
		t.Fatalf("Start returned %T, want Wait", instr)
	}
}

func eventsOf(t *testing.T, e *Engine, runID string) []journal.Event {
	t.Helper()
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, runID)
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// TestPoll_UnroutedTokenKeepsPolling is the heart of the "unrouted but
// well-formed token" ruling: PENDING is a perfectly well-formed §B.1 line
// that names no declared outcome, so the loop must simply run again on the
// next every: tick — no submit, no transition, no journal mutation at all
// while it is still pending.
func TestPoll_UnroutedTokenKeepsPolling(t *testing.T) {
	e := newTestEngine(t, pollWorkflow)
	clock := newFakeClock()
	clock.install(e)
	writeStatus(t, e, "PENDING")
	startParked(t, e, "run1")

	parkedEvents := len(eventsOf(t, e, "run1"))

	var iters []PollIteration
	instr, err := e.Poll("run1", "wait_for_build", func(it PollIteration) {
		iters = append(iters, it)
		if it.N <= 2 {
			// Nothing may have been journalled yet: an unrouted iteration
			// does not submit.
			if got := len(eventsOf(t, e, "run1")); got != parkedEvents {
				t.Errorf("iteration %d: journal grew from %d to %d events; an unrouted poll must not write", it.N, parkedEvents, got)
			}
		}
		if it.N == 2 {
			writeStatus(t, e, "PASSED build_status=PASSED")
		}
	})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(iters) < 3 {
		t.Fatalf("poll ran %d iterations, want at least 3 (two pending, then the routed one)", len(iters))
	}
	for _, it := range iters[:2] {
		if it.Routed {
			t.Errorf("iteration %d reported Routed for token %q; PENDING is not a declared outcome", it.N, it.Token)
		}
		if it.Token != "PENDING" {
			t.Errorf("iteration %d token = %q, want PENDING", it.N, it.Token)
		}
	}
	last := iters[len(iters)-1]
	if !last.Routed || last.Token != "PASSED" {
		t.Errorf("last iteration = %+v, want a routed PASSED", last)
	}

	d, ok := instr.(Dispatch)
	if !ok {
		t.Fatalf("Poll returned %T, want Dispatch for summarize_build", instr)
	}
	if d.Step != "summarize_build" {
		t.Errorf("dispatched %q, want summarize_build", d.Step)
	}

	rs, err := journal.Replay(eventsOf(t, e, "run1"))
	if err != nil {
		t.Fatal(err)
	}
	if rs.State["build_status"] != "PASSED" {
		t.Errorf("build_status = %v, want PASSED: the routed line's pairs payload must be written to state", rs.State["build_status"])
	}
}

// TestPoll_RoutedTokenOnFirstIteration: the first iteration already carries a
// routed token, so pawl poll submits for itself immediately and prints what
// pawl submit would have (design/format-spec.md §13).
func TestPoll_RoutedTokenOnFirstIteration(t *testing.T) {
	e := newTestEngine(t, pollWorkflow)
	newFakeClock().install(e)
	writeStatus(t, e, "PASSED build_status=PASSED")
	startParked(t, e, "run1")

	n := 0
	instr, err := e.Poll("run1", "wait_for_build", func(PollIteration) { n++ })
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if n != 1 {
		t.Errorf("poll ran %d iterations, want exactly 1", n)
	}
	if d, ok := instr.(Dispatch); !ok || d.Step != "summarize_build" {
		t.Fatalf("Poll returned %#v, want a Dispatch for summarize_build", instr)
	}

	// The transition really happened, with the routed outcome.
	var transitions int
	for _, ev := range eventsOf(t, e, "run1") {
		if ev.Kind == journal.KindTransition && ev.Step == "wait_for_build" {
			transitions++
			if ev.Outcome != "PASSED" || ev.Target != "summarize_build" {
				t.Errorf("transition = %s -> %s, want PASSED -> summarize_build", ev.Outcome, ev.Target)
			}
		}
	}
	if transitions != 1 {
		t.Errorf("wait step recorded %d transitions, want 1", transitions)
	}
}

// TestPoll_TimeoutRoutesTimeoutOutcome: the status file never reports, so the
// loop runs until timeout: expires and then routes the reserved timeout
// outcome (DESIGN.md §3) — on the fake clock, instantly.
func TestPoll_TimeoutRoutesTimeoutOutcome(t *testing.T) {
	e := newTestEngine(t, pollWorkflow)
	clock := newFakeClock()
	clock.install(e)
	writeStatus(t, e, "PENDING")
	startParked(t, e, "run1")

	var iters []PollIteration
	instr, err := e.Poll("run1", "wait_for_build", func(it PollIteration) { iters = append(iters, it) })
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok {
		t.Fatalf("Poll returned %T, want Terminal", instr)
	}
	if term.Status != "blocked" || term.Outcome != "timeout" {
		t.Errorf("terminal = %+v, want blocked/timeout", term)
	}
	if len(iters) == 0 || !iters[len(iters)-1].TimedOut {
		t.Errorf("no iteration reported TimedOut: %+v", iters)
	}
	// 10s timeout at 1s every: ~10 sleeps, not 1 and not 600.
	if len(clock.slept) < 5 || len(clock.slept) > 12 {
		t.Errorf("slept %d times (%v), want roughly timeout/every", len(clock.slept), clock.slept)
	}
	for _, d := range clock.slept {
		if d != time.Second {
			t.Errorf("slept %v, want the step's every: of 1s", d)
		}
	}
}

// TestPoll_TimeoutClockRunsFromStepEnter: the deadline is measured from when
// the run parked (the STEP_ENTER's own timestamp), not from when pawl poll
// happened to be invoked — otherwise a poller restarted after a crash would
// reset an hours-long deadline every time.
func TestPoll_TimeoutClockRunsFromStepEnter(t *testing.T) {
	e := newTestEngine(t, pollWorkflow)
	clock := newFakeClock()
	clock.install(e)
	writeStatus(t, e, "PENDING")
	startParked(t, e, "run1")

	// Simulate a poller starting 10s after the run parked: the deadline has
	// already passed, so it must route timeout without ever sleeping.
	clock.now = clock.now.Add(11 * time.Second)

	instr, err := e.Poll("run1", "wait_for_build", func(PollIteration) {})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Outcome != "timeout" {
		t.Fatalf("Poll returned %#v, want a timeout Terminal", instr)
	}
	if len(clock.slept) != 0 {
		t.Errorf("slept %v; an already-expired deadline must route timeout immediately", clock.slept)
	}
}

// TestPoll_ExitsEarlyWhenNotCurrentStep: "it exits early, doing nothing, if
// the run directory has gone or the run's current step is no longer this
// step" (DESIGN.md §3). Doing nothing means not one journal record.
func TestPoll_ExitsEarlyWhenNotCurrentStep(t *testing.T) {
	e := newTestEngine(t, pollWorkflow)
	newFakeClock().install(e)
	writeStatus(t, e, "PASSED build_status=PASSED")
	startParked(t, e, "run1")
	before := len(eventsOf(t, e, "run1"))

	instr, err := e.Poll("run1", "kick_off", func(PollIteration) {
		t.Error("poll: ran an iteration against a step the run is not parked at")
	})
	if !errors.Is(err, ErrPollNotCurrent) {
		t.Fatalf("Poll error = %v, want ErrPollNotCurrent", err)
	}
	if instr != nil {
		t.Errorf("Poll returned instruction %#v, want nil", instr)
	}
	if got := len(eventsOf(t, e, "run1")); got != before {
		t.Errorf("journal grew from %d to %d events; an early exit must do nothing", before, got)
	}
}

// TestPoll_ExitsEarlyOnABlockedRun: a wait step whose outcome routed to
// blocked leaves the run's cursor back on that same wait step (Replay's
// blocked-resume rule), and a blocked RUN_END is not terminal — so without an
// explicit check, a second pawl poll would happily re-run the whole wait and
// journal a *second* transition and RUN_END for a run that is paused awaiting
// a person. A BLOCKED run resumes only through pawl run's
// RESUME{intervention: true} (DESIGN.md §4), never through the poller.
func TestPoll_ExitsEarlyOnABlockedRun(t *testing.T) {
	e := newTestEngine(t, pollWorkflow)
	newFakeClock().install(e)
	writeStatus(t, e, "FAILED build_status=FAILED")
	startParked(t, e, "run1")

	instr, err := e.Poll("run1", "wait_for_build", func(PollIteration) {})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("Poll returned %#v, want a blocked Terminal", instr)
	}
	blocked := len(eventsOf(t, e, "run1"))

	_, err = e.Poll("run1", "wait_for_build", func(PollIteration) {
		t.Error("poll: ran an iteration against a BLOCKED run")
	})
	if !errors.Is(err, ErrPollNotCurrent) {
		t.Fatalf("second Poll error = %v, want ErrPollNotCurrent", err)
	}
	if got := len(eventsOf(t, e, "run1")); got != blocked {
		t.Errorf("journal grew from %d to %d events; polling a blocked run must do nothing", blocked, got)
	}
}

// TestPoll_ExitsEarlyWhenRunDirectoryIsGone is the other half of the same
// sentence.
func TestPoll_ExitsEarlyWhenRunDirectoryIsGone(t *testing.T) {
	e := newTestEngine(t, pollWorkflow)
	newFakeClock().install(e)
	writeStatus(t, e, "PASSED build_status=PASSED")
	startParked(t, e, "run1")
	if err := os.RemoveAll(journal.RunDir(e.Root, e.Workflow.Workflow, "run1")); err != nil {
		t.Fatal(err)
	}

	_, err := e.Poll("run1", "wait_for_build", func(PollIteration) {
		t.Error("poll: ran an iteration against a vanished run directory")
	})
	if !errors.Is(err, ErrPollNotCurrent) {
		t.Fatalf("Poll error = %v, want ErrPollNotCurrent", err)
	}
}

// TestPoll_SubmitStillRefusedMidLoop: pawl poll holds no lock between
// iterations (a wait is hours long), so a model that ignores the WAIT block
// and runs pawl submit anyway must still hit the wait-step refusal, not a
// lock error and certainly not a successful submit racing the poller.
func TestPoll_SubmitStillRefusedMidLoop(t *testing.T) {
	e := newTestEngine(t, pollWorkflow)
	clock := newFakeClock()
	clock.install(e)
	writeStatus(t, e, "PENDING")
	startParked(t, e, "run1")

	checked := false
	_, err := e.Poll("run1", "wait_for_build", func(it PollIteration) {
		if it.N != 1 {
			return
		}
		checked = true
		_, serr := e.Submit("run1", "wait_for_build", 1, json.RawMessage(`{"build_status":"PASSED"}`))
		if serr == nil {
			t.Fatal("pawl submit succeeded mid-poll; it must be refused")
		}
		if !strings.Contains(serr.Error(), "pawl poll") {
			t.Errorf("mid-poll refusal %q does not name pawl poll", serr.Error())
		}
	})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !checked {
		t.Fatal("observer never ran")
	}
}

// TestPoll_NonZeroExitIsFailure pins design/format-spec.md §B.1's
// "non-zero exit is always failure, whatever was printed" for the wait kind
// too — it is one grammar for both kinds that run a shell command.
func TestPoll_NonZeroExitIsFailure(t *testing.T) {
	e := newTestEngine(t, strings.Replace(pollWorkflow, "poll: cat status", "poll: \"false\"", 1))
	newFakeClock().install(e)
	startParked(t, e, "run1")

	instr, err := e.Poll("run1", "wait_for_build", func(PollIteration) {})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok {
		t.Fatalf("Poll returned %T, want Terminal (failure routes to blocked)", instr)
	}
	if term.Status != "blocked" || term.Outcome != "failure" {
		t.Errorf("terminal = %+v, want blocked/failure", term)
	}
}
