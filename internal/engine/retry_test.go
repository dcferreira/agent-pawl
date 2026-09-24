package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// counterRun is a run: command that fails (exit 1) until it has been
// invoked failsBefore times, then succeeds (exit 0), by counting invocations
// in a file under the engine's working-copy root (mirrors poll_test.go's
// writeStatus/e.Root convention).
func counterRun(counterFile string, failsBefore int) string {
	return "n=$(cat " + counterFile + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " +
		counterFile + "; if [ $n -le " + itoa(failsBefore) + " ]; then echo bad-try-$n >&2; exit 1; fi; exit 0"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func readCounter(t *testing.T, e *Engine, name string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.Root, name))
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range strings.TrimSpace(string(data)) {
		n = n*10 + int(c-'0')
	}
	return n
}

// TestRetry_DeterministicSucceedsAfterHardRetry: run: hard-fails (non-zero
// exit) on its first try and succeeds on its second; retry: {max_attempts: 3,
// backoff: 10s} must retry the body in-place, sleeping backoff*1 = 10s once
// (via the injected fake Sleep — no real sleep), and the step must still
// transition on "done" as if it had succeeded on the first try: no extra
// attempts:/max_visits: consumed.
func TestRetry_DeterministicSucceedsAfterHardRetry(t *testing.T) {
	const yamlTmpl = `
workflow: retry-hard-failure
start: a
steps:
  - id: a
    kind: deterministic
    run: '%s'
    retry: {max_attempts: 3, backoff: 10s}
    max_visits: 1
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, sprintfYAML(yamlTmpl, counterRun("counter", 1)))
	clock := newFakeClock()
	clock.install(e)

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} (the retry should have succeeded)", instr)
	}
	if n := readCounter(t, e, "counter"); n != 2 {
		t.Errorf("run: was invoked %d times, want exactly 2 (1 hard failure + 1 successful retry)", n)
	}
	if len(clock.slept) != 1 || clock.slept[0] != 10*time.Second {
		t.Errorf("slept = %v, want exactly one sleep of 10s (backoff * try 1)", clock.slept)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var enters int
	var sawHardRetry1 bool
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter && ev.Step == "a" {
			enters++
			if ev.HardRetry == 1 {
				sawHardRetry1 = true
			}
			if ev.Retry {
				t.Errorf("STEP_ENTER for a hard-failure retry must not set Retry (that means attempts:-level re-entry): %+v", ev)
			}
		}
	}
	if enters != 2 {
		t.Errorf("STEP_ENTER count = %d, want 2 (the first try, then the hard-retry)", enters)
	}
	if !sawHardRetry1 {
		t.Error("no STEP_ENTER{HardRetry: 1} recorded for the retried try")
	}

	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Visits["a"] != 1 {
		t.Errorf("Visits[a] = %d, want 1: a hard-failure retry must not consume max_visits:", rs.Visits["a"])
	}
}

// TestRetry_DeterministicLogsProgressToStderr: a deterministic retry: sleeps
// and re-execs synchronously inside one blocking Start/Submit/Poll call with
// nothing on stdout — Engine.Stderr, when set, must get one progress line
// per retried try so a long backoff isn't silent. A nil Stderr (the default,
// and every other test in this file) must stay a no-op — covered implicitly
// by every other test here never setting it and never panicking.
func TestRetry_DeterministicLogsProgressToStderr(t *testing.T) {
	const yamlTmpl = `
workflow: retry-hard-failure-log
start: a
steps:
  - id: a
    kind: deterministic
    run: '%s'
    retry: {max_attempts: 3, backoff: 10s}
    max_visits: 1
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, sprintfYAML(yamlTmpl, counterRun("counter", 1)))
	clock := newFakeClock()
	clock.install(e)
	var stderr bytes.Buffer
	e.Stderr = &stderr

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}", instr)
	}

	got := stderr.String()
	want := `pawl: step "a" hard-failed (try 1 of 3); retrying in 10s` + "\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestRetry_DeterministicExhaustsAndRoutesFailure: every try hard-fails;
// once max_attempts tries are spent, the step behaves exactly as an
// un-retried hard failure would — the reserved failure outcome, routed (here
// with no catch:) to blocked.
func TestRetry_DeterministicExhaustsAndRoutesFailure(t *testing.T) {
	const yamlTmpl = `
workflow: retry-exhausted
start: a
steps:
  - id: a
    kind: deterministic
    run: '%s'
    retry: {max_attempts: 3, backoff: 1s}
    next: done
terminal:
  done: {status: ok}
  blocked: {status: blocked, message: "paused: ${blocked_reason}"}
`
	e := newTestEngine(t, sprintfYAML(yamlTmpl, counterRun("counter", 99)))
	clock := newFakeClock()
	clock.install(e)

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} once retries are exhausted", instr)
	}
	if n := readCounter(t, e, "counter"); n != 3 {
		t.Errorf("run: was invoked %d times, want exactly 3 (max_attempts:)", n)
	}
	// backoff * 1, then backoff * 2 — linear, no jitter.
	want := []time.Duration{1 * time.Second, 2 * time.Second}
	if len(clock.slept) != len(want) {
		t.Fatalf("slept = %v, want %v", clock.slept, want)
	}
	for i, d := range want {
		if clock.slept[i] != d {
			t.Errorf("slept[%d] = %v, want %v", i, clock.slept[i], d)
		}
	}
}

// TestRetry_DeterministicResumeMidBackoff simulates a real crash mid
// backoff-sleep: advanceDeterministic now journals STEP_ENTER{HardRetry: n}
// BEFORE sleeping (not after), so the journal left behind by a process
// killed during that sleep ends with [STEP_ENTER{HardRetry: 0}, the fresh
// try's own failure-diagnostic POSTCONDITION{OK: false}, STEP_ENTER{
// HardRetry: 1}] — recording the *intent* to run hard-retry try 1, which
// never actually ran before the crash. Resume must continue the retry loop
// at that SAME hard-retry count and re-run the try that never ran (never
// advancing past it on a crash, exactly as attempts:-level retries already
// behave — TestAttempts_NotAdvancedByCrash, and never re-running hard-retry
// try 0's own already-completed, already-failed body), so total tries
// across the crash never exceed max_attempts:.
func TestRetry_DeterministicResumeMidBackoff(t *testing.T) {
	const yamlTmpl = `
workflow: retry-resume
start: a
steps:
  - id: a
    kind: deterministic
    run: '%s'
    retry: {max_attempts: 3, backoff: 5s}
    next: done
terminal: {done: {status: ok}}
`
	// The command succeeds only on its 3rd invocation: try 1 (fresh),
	// hard-retry 1 (still fails), hard-retry 2 (succeeds) — but the journal
	// below stops right after hard-retry 1's STEP_ENTER, before it even ran,
	// simulating the crash mid loop.
	w := loadWorkflow(t, sprintfYAML(yamlTmpl, counterRun("counter", 2)))
	stateDir := t.TempDir()
	t.Setenv(journal.EnvStateDir, stateDir)
	root := t.TempDir()
	e := &Engine{Workflow: w, Root: root, Timeout: 5 * time.Second}
	clock := newFakeClock()
	clock.install(e)

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
	mustAppend(journal.Event{Kind: journal.KindStepEnter, RunID: runID, Step: "a", Attempt: 1, AttemptKey: ""})
	// The fresh try hard-failed (journalFailureDiagnostic's own shape: a
	// POSTCONDITION carrying the diagnostic text, no AttemptKey — a hard
	// failure is never retried by attempts:).
	mustAppend(journal.Event{Kind: journal.KindPostcondition, RunID: runID, Step: "a", Attempt: 1, OK: false, Text: "bad-try-1"})
	// The retry loop journaled its intent to run hard-retry try 1 (this
	// STEP_ENTER) BEFORE sleeping backoff*1, per advanceDeterministic's
	// journal-then-sleep ordering — and the simulated crash leaves the
	// trail cold right there, mid backoff-sleep, before try 1's body ever
	// ran or its result (success or failure) was ever journaled.
	mustAppend(journal.Event{Kind: journal.KindStepEnter, RunID: runID, Step: "a", Attempt: 1, AttemptKey: "", HardRetry: 1})
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	// The counter file must reflect that the body already ran once (the
	// fresh try) before the crash — same as if the real engine had run it.
	if err := os.WriteFile(filepath.Join(root, "counter"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	instr, err := e.Resume(runID, false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} (hard-retry 2 should have succeeded)", instr)
	}
	if n := readCounter(t, e, "counter"); n != 3 {
		t.Errorf("run: total invocations = %d, want exactly 3 (fresh try, re-run of hard-retry 1, hard-retry 2) — never more than max_attempts:", n)
	}

	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var hardRetry1Enters, hardRetry2Enters int
	for _, ev := range events {
		if ev.Kind != journal.KindStepEnter || ev.Step != "a" {
			continue
		}
		switch ev.HardRetry {
		case 1:
			hardRetry1Enters++
		case 2:
			hardRetry2Enters++
		}
	}
	if hardRetry1Enters != 2 {
		t.Errorf("STEP_ENTER{HardRetry:1} count = %d, want 2 (one before the simulated crash, one from resume) — a crash must not advance the hard-retry count", hardRetry1Enters)
	}
	if hardRetry2Enters != 1 {
		t.Errorf("STEP_ENTER{HardRetry:2} count = %d, want exactly 1", hardRetry2Enters)
	}
	// Resume re-runs hard-retry try 1 immediately (no sleep: that backoff
	// already happened before the simulated crash); only once it fails
	// again does the loop sleep backoff*2 before hard-retry try 2, which
	// then succeeds — exactly one sleep, never a third try's worth.
	want := []time.Duration{10 * time.Second}
	if len(clock.slept) != len(want) {
		t.Fatalf("slept = %v, want %v", clock.slept, want)
	}
}

// TestRetry_DeterministicResumeMidBackoffPreservesLastErrorAndVisits pins the
// invariants a crash-mid-backoff resume must uphold once STEP_ENTER{
// HardRetry: n} is journaled before the sleep (see
// TestRetry_DeterministicResumeMidBackoff): given a journal ending in
// [STEP_ENTER{HardRetry: 0}, the fresh try's own failure-diagnostic
// POSTCONDITION{OK: false}, STEP_ENTER{HardRetry: 1}] (the crash landed
// mid backoff-sleep, before hard-retry try 1 ever ran) —
//   - total body runs across the crash never exceed max_attempts: (here 2:
//     the fresh try before the crash, plus the resumed hard-retry try 1 —
//     hard-retry try 1 is never run twice);
//   - ${last_error} seen by the resumed try equals what the attempt's first
//     (fresh) try itself saw — empty here, never the fresh try's own
//     "boom-try-1" diagnostic (rs.AttemptLastError must be reconstructed by
//     Replay from the attempt's original STEP_ENTER{HardRetry: 0}, not
//     re-frozen by the STEP_ENTER{HardRetry: 1} RESUME lands on);
//   - the step declares no postcondition:, so last_error must still end up
//     cleared once the recovered retry succeeds (the synthetic
//     POSTCONDITION{OK: true} runDeterministicAttempt's caller journals for
//     a hardRetried attempt with no declared postcondition:); and
//   - Visits["a"] is 1 (a hard-retry resume must never double-count a
//     visit).
func TestRetry_DeterministicResumeMidBackoffPreservesLastErrorAndVisits(t *testing.T) {
	const yamlTmpl = `
workflow: retry-resume-last-error
start: a
steps:
  - id: a
    kind: deterministic
    run: '%s'
    retry: {max_attempts: 3, backoff: 5s}
    next: done
terminal: {done: {status: ok}}
`
	// n=1 (the fresh try, run before the simulated crash) hard-fails,
	// printing "boom-try-1". n=2 (the resumed hard-retry try 1) succeeds and
	// records the ${last_error} it was rendered with, so the test can assert
	// it against what the fresh try itself saw (empty — nothing failed
	// before this step ever ran).
	run := "n=$(cat counter 2>/dev/null || echo 0); n=$((n+1)); echo $n > counter; " +
		"if [ $n -eq 1 ]; then echo boom-try-1 >&2; exit 1; fi; " +
		"echo ${last_error} > seen.txt; exit 0"
	w := loadWorkflow(t, sprintfYAML(yamlTmpl, run))
	stateDir := t.TempDir()
	t.Setenv(journal.EnvStateDir, stateDir)
	root := t.TempDir()
	e := &Engine{Workflow: w, Root: root, Timeout: 5 * time.Second}
	clock := newFakeClock()
	clock.install(e)

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
	mustAppend(journal.Event{Kind: journal.KindStepEnter, RunID: runID, Step: "a", Attempt: 1, AttemptKey: ""})
	mustAppend(journal.Event{Kind: journal.KindPostcondition, RunID: runID, Step: "a", Attempt: 1, OK: false, Text: "boom-try-1"})
	mustAppend(journal.Event{Kind: journal.KindStepEnter, RunID: runID, Step: "a", Attempt: 1, AttemptKey: "", HardRetry: 1})
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "counter"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	instr, err := e.Resume(runID, false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} (the resumed hard-retry try should have succeeded)", instr)
	}
	if n := readCounter(t, e, "counter"); n != 2 {
		t.Errorf("run: total invocations = %d, want exactly 2 (fresh try before the crash, resumed hard-retry try 1) — never more than max_attempts: 3, and never a re-run of an already-completed try", n)
	}

	seen, err := os.ReadFile(filepath.Join(root, "seen.txt"))
	if err != nil {
		t.Fatalf("reading seen.txt (should have been written by the resumed try): %v", err)
	}
	if got := strings.TrimSpace(string(seen)); got != "" {
		t.Errorf("last_error seen by the resumed try = %q, want empty (what the attempt's own first try saw — never the fresh try's own %q diagnostic)", got, "boom-try-1")
	}

	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if rs.LastError != "" {
		t.Errorf("last_error = %q, want empty: a recovered crash-mid-backoff hard-retry must not leave a stale diagnostic behind", rs.LastError)
	}
	if rs.Visits["a"] != 1 {
		t.Errorf("Visits[a] = %d, want 1: a crash-mid-backoff resume must not double-count a visit", rs.Visits["a"])
	}
}

func sprintfYAML(tmpl, run string) string {
	return strings.Replace(tmpl, "%s", run, 1)
}

// pollRetryWorkflowTmpl mirrors poll_test.go's pollWorkflow, adding retry: to
// the wait step so its poll: command's own hard failures (non-zero exit) can
// be retried in-loop.
const pollRetryWorkflowTmpl = `
workflow: poll-retry
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
    poll: '%s'
    every: 1s
    timeout: 30s
    emits: pairs
    writes: {build_status: {type: string}}
    retry: {max_attempts: %d, backoff: %s}
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

func pollRetryWorkflow(t *testing.T, poll string, maxAttempts int, backoff string) *Engine {
	t.Helper()
	yaml := strings.Replace(pollRetryWorkflowTmpl, "%s", poll, 1)
	yaml = strings.Replace(yaml, "%d", itoa(maxAttempts), 1)
	yaml = strings.Replace(yaml, "%s", backoff, 1)
	return newTestEngine(t, yaml)
}

// counterPoll is a poll: command that fails (non-zero exit) until it has
// been invoked failsBefore times, then prints successLine and exits 0.
func counterPoll(counterFile string, failsBefore int, successLine string) string {
	return "n=$(cat " + counterFile + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " +
		counterFile + "; if [ $n -le " + itoa(failsBefore) + " ]; then echo bad-try-$n >&2; exit 1; fi; echo " + successLine
}

// TestRetry_WaitSucceedsAfterHardRetry: poll: hard-fails (non-zero exit) on
// its first tick and succeeds on its second. retry: {max_attempts: 3,
// backoff: 2s} must retry the tick in-loop — sleeping backoff*1 = 2s, not
// every: 1s — without ending the loop, and complete exactly as a
// first-tick success would.
func TestRetry_WaitSucceedsAfterHardRetry(t *testing.T) {
	e := pollRetryWorkflow(t, counterPoll("pcounter", 1, "PASSED build_status=PASSED"), 3, "2s")
	clock := newFakeClock()
	clock.install(e)
	startParked(t, e, "run1")

	var iters []PollIteration
	instr, err := e.Poll("run1", "wait_for_build", func(it PollIteration) { iters = append(iters, it) })
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	d, ok := instr.(Dispatch)
	if !ok || d.Step != "summarize_build" {
		t.Fatalf("Poll returned %#v, want a Dispatch for summarize_build (the retry should have succeeded)", instr)
	}
	if len(iters) != 2 {
		t.Fatalf("poll ran %d iterations, want exactly 2 (the hard failure, then the successful retry)", len(iters))
	}
	if !iters[0].Routed || iters[0].Token != "failure" {
		t.Errorf("iteration 1 = %+v, want a routed failure", iters[0])
	}
	if len(clock.slept) != 1 || clock.slept[0] != 2*time.Second {
		t.Errorf("slept = %v, want exactly one sleep of 2s (backoff * try 1, not every:)", clock.slept)
	}

	rs, err := journal.Replay(eventsOf(t, e, "run1"))
	if err != nil {
		t.Fatal(err)
	}
	if rs.State["build_status"] != "PASSED" {
		t.Errorf("build_status = %v, want PASSED", rs.State["build_status"])
	}
}

// TestRetry_WaitExhaustsAndRoutesFailure: poll: always hard-fails;
// max_attempts: 2 means exactly one retry is spent before the loop ends on
// the reserved failure outcome — identical to what an un-retried wait step
// does on its very first hard failure, just one tick later.
func TestRetry_WaitExhaustsAndRoutesFailure(t *testing.T) {
	e := pollRetryWorkflow(t, counterPoll("pcounter", 99, "unreachable"), 2, "1s")
	clock := newFakeClock()
	clock.install(e)
	startParked(t, e, "run1")

	var iters []PollIteration
	instr, err := e.Poll("run1", "wait_for_build", func(it PollIteration) { iters = append(iters, it) })
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" || term.Outcome != "failure" {
		t.Fatalf("Poll returned %#v, want Terminal{blocked/failure}", instr)
	}
	if len(iters) != 2 {
		t.Fatalf("poll ran %d iterations, want exactly 2 (max_attempts:)", len(iters))
	}
	if len(clock.slept) != 1 || clock.slept[0] != 1*time.Second {
		t.Errorf("slept = %v, want exactly one sleep of 1s (backoff * try 1)", clock.slept)
	}
}

// TestRetry_WaitConsecutiveCounterResetsOnCleanTick: the hard-failure streak
// is CONSECUTIVE (design/format-spec.md §B.16) — an unrouted "not yet" tick
// between two failures resets it, so with max_attempts: 2 a fail/clean/fail/
// success sequence must still reach and route the eventual success, never
// ending early on the second, non-consecutive failure.
func TestRetry_WaitConsecutiveCounterResetsOnCleanTick(t *testing.T) {
	poll := "n=$(cat pcounter 2>/dev/null || echo 0); n=$((n+1)); echo $n > pcounter; " +
		"if [ $n -eq 1 ]; then echo bad1 >&2; exit 1; " +
		"elif [ $n -eq 2 ]; then echo PENDING; " +
		"elif [ $n -eq 3 ]; then echo bad2 >&2; exit 1; " +
		"else echo PASSED build_status=PASSED; fi"
	e := pollRetryWorkflow(t, poll, 2, "1s")
	clock := newFakeClock()
	clock.install(e)
	startParked(t, e, "run1")

	var iters []PollIteration
	instr, err := e.Poll("run1", "wait_for_build", func(it PollIteration) { iters = append(iters, it) })
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	d, ok := instr.(Dispatch)
	if !ok || d.Step != "summarize_build" {
		t.Fatalf("Poll returned %#v, want a Dispatch for summarize_build: the clean PENDING tick must have reset the streak, so the second failure (non-consecutive) must not itself exhaust max_attempts: 2", instr)
	}
	if len(iters) != 4 {
		t.Fatalf("poll ran %d iterations, want exactly 4 (fail, PENDING, fail, success)", len(iters))
	}
	want := []time.Duration{1 * time.Second, 1 * time.Second, 1 * time.Second}
	if len(clock.slept) != len(want) {
		t.Fatalf("slept = %v, want %v", clock.slept, want)
	}
}

// TestRetry_WaitExitZeroFailureTokenIsNotHardFailure pins a review finding:
// a routed "failure" TOKEN from a *successful* (exit-0) poll: — the
// author's own script explicitly printing the reserved failure token, only
// possible when it is routable via catch:/outcomes: — is not one of
// §B.16's three hard-failure cases (poll:'s own non-zero exit, the
// wall-clock timeout, or an unintelligible payload). It is an ordinary
// clean routed tick that happens to route to failure, so retry: must never
// retry it and it must complete on the very first tick with the same
// diagnostic an un-retried wait step always produced — exactly as it did
// before retry: existed.
func TestRetry_WaitExitZeroFailureTokenIsNotHardFailure(t *testing.T) {
	const yaml = `
workflow: poll-retry-failure-token
start: kick_off
state:
  build_status: {type: string, default: ""}
steps:
  - id: kick_off
    kind: deterministic
    run: "true"
    next: wait_for_build
  - id: wait_for_build
    kind: wait
    poll: "echo failure diag-message"
    every: 1s
    timeout: 30s
    retry: {max_attempts: 3, backoff: 5s}
    outcomes:
      PASSED: done
      timeout: blocked
    catch: [{on: failure, next: blocked}]
terminal:
  done:    {status: ok}
  blocked: {status: blocked, message: "paused: ${blocked_reason}"}
`
	e := newTestEngine(t, yaml)
	clock := newFakeClock()
	clock.install(e)
	startParked(t, e, "run1")

	var iters []PollIteration
	instr, err := e.Poll("run1", "wait_for_build", func(it PollIteration) { iters = append(iters, it) })
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("Poll returned %#v, want Terminal{blocked} on the very first tick", instr)
	}
	rs, err := journal.Replay(eventsOf(t, e, "run1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rs.LastError, "diag-message") {
		t.Errorf("last_error = %q, want it to carry the poll: script's own diagnostic (diag-message)", rs.LastError)
	}
	if len(iters) != 1 {
		t.Fatalf("poll ran %d iterations, want exactly 1: an exit-0 failure token must not be retried", len(iters))
	}
	if len(clock.slept) != 0 {
		t.Errorf("slept = %v, want no sleep at all: an exit-0 failure token is a clean routed tick, not a hard failure", clock.slept)
	}
}

// TestRetry_DeterministicExitZeroFailureTokenIsNotHardFailure pins a review
// finding: a deterministic run: that exits 0 but prints the reserved
// "failure" TOKEN on its last line (only possible when the step declares an
// author-named outcome, making TOKEN parsing active) is not one of §B.16's
// three hard-failure cases (non-zero exit, the wall-clock timeout, or
// unintelligible stdout) — mirrors TestRetry_WaitExitZeroFailureTokenIsNotHardFailure.
// retry: must never retry it, and it must route on the very first try with
// the run:'s own diagnostic still visible as ${last_error}.
func TestRetry_DeterministicExitZeroFailureTokenIsNotHardFailure(t *testing.T) {
	const yaml = `
workflow: det-retry-failure-token
start: a
steps:
  - id: a
    kind: deterministic
    run: "echo diag-message >&2; echo failure"
    retry: {max_attempts: 3, backoff: 10s}
    outcomes:
      PASSED: done
    catch: [{on: failure, next: blocked}]
terminal:
  done:    {status: ok}
  blocked: {status: blocked, message: "paused: ${blocked_reason}"}
`
	e := newTestEngine(t, yaml)
	clock := newFakeClock()
	clock.install(e)

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} on the very first try", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var enters int
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter && ev.Step == "a" {
			enters++
		}
	}
	if enters != 1 {
		t.Errorf("STEP_ENTER count = %d, want exactly 1: an exit-0 failure token must not be retried", enters)
	}
	if len(clock.slept) != 0 {
		t.Errorf("slept = %v, want no sleep at all: an exit-0 failure token is a clean routed tick, not a hard failure", clock.slept)
	}

	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rs.LastError, "diag-message") {
		t.Errorf("last_error = %q, want it to carry the run:'s own diagnostic (diag-message)", rs.LastError)
	}
}

// TestRetry_DeterministicExitZeroFailureTokenSkipsFailingPostcondition pins
// the exact pre-retry: routing for an exit-0 author-printed reserved
// "failure" token: it is hardFailed (routes straight to routeReserved, the
// same as a non-zero exit or a timeout always did) and must never run a
// postcondition or consume an attempts: retry — even when the step declares
// both a postcondition: that would fail and attempts: > 1 to retry under.
// If the token instead fell through to postcondition evaluation (the bug
// this regression pins), the failing postcondition would trigger an
// attempts:-level retry (a second STEP_ENTER) and last_error would carry
// the postcondition's own failure text instead of the run:'s diagnostic.
func TestRetry_DeterministicExitZeroFailureTokenSkipsFailingPostcondition(t *testing.T) {
	const yaml = `
workflow: det-retry-failure-token-postcondition
start: a
steps:
  - id: a
    kind: deterministic
    run: "echo diag-message >&2; echo failure"
    retry: {max_attempts: 3, backoff: 10s}
    attempts: 3
    postcondition: "false"
    outcomes:
      PASSED: done
    catch: [{on: failure, next: blocked}]
terminal:
  done:    {status: ok}
  blocked: {status: blocked, message: "paused: ${blocked_reason}"}
`
	e := newTestEngine(t, yaml)
	clock := newFakeClock()
	clock.install(e)

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} on the very first try", instr)
	}

	events := eventsOf(t, e, "run1")
	var enters int
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter && ev.Step == "a" {
			enters++
		}
	}
	if enters != 1 {
		t.Errorf("STEP_ENTER count = %d, want exactly 1: a failing postcondition: must never trigger an attempts: retry for a hardFailed exit-0 failure token", enters)
	}
	if len(clock.slept) != 0 {
		t.Errorf("slept = %v, want no sleep at all", clock.slept)
	}

	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rs.LastError, "diag-message") {
		t.Errorf("last_error = %q, want it to carry the run:'s own diagnostic (diag-message), not the postcondition's failure text", rs.LastError)
	}
}

// TestRetry_DeterministicHardRetrySuccessClearsLastError pins a review
// finding: a try that hard-fails (journaling a failure diagnostic into
// last_error, per I1) and is then retried successfully by retry: must not
// leave that earlier try's diagnostic stuck in ${last_error} — the step
// here declares no postcondition:, so nothing else would otherwise journal
// the POSTCONDITION{OK:true} Replay needs to clear it.
func TestRetry_DeterministicHardRetrySuccessClearsLastError(t *testing.T) {
	const yamlTmpl = `
workflow: retry-hard-failure-clears-last-error
start: a
steps:
  - id: a
    kind: deterministic
    run: '%s'
    retry: {max_attempts: 3, backoff: 10s}
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, sprintfYAML(yamlTmpl, counterRun("counter", 1)))
	clock := newFakeClock()
	clock.install(e)

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} (the retry should have succeeded)", instr)
	}

	rs, err := journal.Replay(eventsOf(t, e, "run1"))
	if err != nil {
		t.Fatal(err)
	}
	if rs.LastError != "" {
		t.Errorf("last_error = %q, want empty: a recovered hard-retry must not leave a stale diagnostic behind", rs.LastError)
	}
	if _, stillBudgeted := rs.Attempts[journal.AttemptRef{Step: "a", Key: ""}]; stillBudgeted {
		t.Errorf("Attempts still holds a budget entry for step %q after a clean non-catch transition; the §B.4 clearing rule should have deleted it", "a")
	}
}

// TestRetry_DeterministicHardRetryDoesNotSeePriorTrysDiagnostic pins a
// review finding: a retry: hard-retry of an attempt must render
// ${last_error} exactly as the attempt's first try saw it — never the
// diagnostic the previous hard-failed try of the *same* attempt just
// journaled (design/format-spec.md §B.16). run: writes ${last_error} to a
// file on its second invocation (after failing hard on its first); since
// nothing failed before this step ever ran, the attempt's first try saw an
// empty last_error, so the retried try must see it empty too, never the
// first try's own "boom-try-1" diagnostic.
func TestRetry_DeterministicHardRetryDoesNotSeePriorTrysDiagnostic(t *testing.T) {
	const yamlTmpl = `
workflow: retry-hard-retry-no-self-diagnostic
start: a
steps:
  - id: a
    kind: deterministic
    run: '%s'
    retry: {max_attempts: 3, backoff: 10s}
    next: done
terminal: {done: {status: ok}}
`
	run := "n=$(cat counter 2>/dev/null || echo 0); n=$((n+1)); echo $n > counter; " +
		"if [ $n -eq 1 ]; then echo boom-try-1 >&2; exit 1; fi; " +
		"echo ${last_error} > seen.txt; exit 0"
	e := newTestEngine(t, sprintfYAML(yamlTmpl, run))
	clock := newFakeClock()
	clock.install(e)

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} (the retry should have succeeded)", instr)
	}
	if n := readCounter(t, e, "counter"); n != 2 {
		t.Fatalf("run: was invoked %d times, want exactly 2 (1 hard failure + 1 successful retry)", n)
	}

	seen, err := os.ReadFile(filepath.Join(e.Root, "seen.txt"))
	if err != nil {
		t.Fatalf("reading seen.txt (should have been written by the second try): %v", err)
	}
	got := strings.TrimSpace(string(seen))
	if got != "" {
		t.Errorf("second try saw ${last_error} = %q, want empty: a retried try must not see the diagnostic its own attempt's previous try just journaled", got)
	}

	// Sanity: the diagnostic really was journaled (I1) — this asserts the
	// fix does not also erase it from the run's own record, only from what
	// a retried try itself observes.
	rs, err := journal.Replay(eventsOf(t, e, "run1"))
	if err != nil {
		t.Fatal(err)
	}
	if rs.LastError != "" {
		t.Errorf("final last_error = %q, want empty: a recovered hard-retry clears last_error (TestRetry_DeterministicHardRetrySuccessClearsLastError)", rs.LastError)
	}
}
