package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// crashedRun hand-builds the journal of a run that crashed with step's
// STEP_ENTER durable and nothing after it (the same simulation
// TestAttempts_NotAdvancedByCrash uses), and returns an engine plus run dir
// ready for Resume. preceding events (after RUN_START, before the crashed
// STEP_ENTER) are appended first.
func crashedRun(t *testing.T, yaml, step string, preceding ...journal.Event) (*Engine, string) {
	t.Helper()
	w := loadWorkflow(t, yaml)
	t.Setenv(journal.EnvStateDir, t.TempDir())
	root := t.TempDir()
	e := &Engine{Workflow: w, Root: root, Timeout: 5 * time.Second}
	const runID = "run1"
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
	defer log.Close()
	evs := append([]journal.Event{{Kind: journal.KindRunStart, RunID: runID}}, preceding...)
	evs = append(evs, journal.Event{Kind: journal.KindStepEnter, RunID: runID, Step: step, Attempt: 1})
	for _, ev := range evs {
		if _, err := log.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	return e, dir
}

func visitsOf(t *testing.T, dir, step string) int {
	t.Helper()
	rs, err := replayDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return rs.Visits[step]
}

// TestResume_CrashDeterministicKeepsVisit: a deterministic step that crashed
// mid-run with max_visits: 1 resumes into its body (not "exhausted"), sees
// ${visits} = 1 exactly as the interrupted try did, and ends with one visit.
func TestResume_CrashDeterministicKeepsVisit(t *testing.T) {
	const yaml = `
workflow: crash-visits
start: a
steps:
  - id: a
    kind: deterministic
    run: "echo ${visits} > seen.txt"
    max_visits: 1
    next: done
terminal: {done: {status: ok}}
`
	e, dir := crashedRun(t, yaml, "a")
	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}: a crash-resume must not exhaust max_visits: 1", instr)
	}
	b, err := os.ReadFile(filepath.Join(e.Root, "seen.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "1" {
		t.Errorf("${visits} in the resumed step = %q, want 1 (same visit as the interrupted try)", got)
	}
	if v := visitsOf(t, dir, "a"); v != 1 {
		t.Errorf("Visits[a] = %d, want 1", v)
	}
}

// TestResume_CrashAgenticAndHumanKeepVisit: same for agentic and human
// dispatch, which append their own STEP_ENTER.
func TestResume_CrashAgenticAndHumanKeepVisit(t *testing.T) {
	cases := map[string]string{
		"agentic": `
workflow: crash-agentic
start: a
state:
  result: {type: string, default: ""}
steps:
  - id: a
    kind: agentic
    description: "do it"
    writes: {result: {type: string}}
    postcondition: "true"
    max_visits: 1
    next: done
terminal: {done: {status: ok}}
`,
		"human": `
workflow: crash-human
start: a
steps:
  - id: a
    kind: human
    question: "ok?"
    options: [yes, no]
    timeout: 24h
    max_visits: 1
    outcomes: {yes: done, no: done, timeout: done}
terminal: {done: {status: ok}}
`,
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			e, dir := crashedRun(t, yaml, "a")
			instr, err := e.Resume("run1", false)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if term, ok := instr.(Terminal); ok {
				t.Fatalf("got %+v, want a dispatch/ask, not a terminal (max_visits: 1 must not exhaust on crash-resume)", term)
			}
			if v := visitsOf(t, dir, "a"); v != 1 {
				t.Errorf("Visits[a] = %d, want 1", v)
			}
		})
	}
}

// TestResume_CrashDoesNotInflateMaxSteps: the workflow-level max_steps total
// (sum of Visits) is not inflated by a crash-resume either: with max_steps: 2
// and a-then-b, resuming a crashed b must still run it.
func TestResume_CrashDoesNotInflateMaxSteps(t *testing.T) {
	const yaml = `
workflow: crash-max-steps
start: a
max_steps: 2
steps:
  - id: a
    kind: deterministic
    run: "true"
    next: b
  - id: b
    kind: deterministic
    run: "true"
    next: done
terminal: {done: {status: ok}}
`
	e, _ := crashedRun(t, yaml, "b",
		journal.Event{Kind: journal.KindStepEnter, RunID: "run1", Step: "a", Attempt: 1},
		journal.Event{Kind: journal.KindTransition, RunID: "run1", Step: "a", Attempt: 1, Target: "b", Outcome: "success"},
	)
	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}", instr)
	}
}

// TestResume_CrashBetweenTransitionAndEnterIsFirstVisit: a crash after a
// TRANSITION but before the target's STEP_ENTER never entered the target, so
// the resume's STEP_ENTER IS its first visit and must still be counted (and
// capped).
func TestResume_CrashBetweenTransitionAndEnterIsFirstVisit(t *testing.T) {
	const yaml = `
workflow: crash-pre-enter
start: a
steps:
  - id: a
    kind: deterministic
    run: "true"
    next: b
  - id: b
    kind: deterministic
    run: "true"
    max_visits: 1
    next: done
terminal: {done: {status: ok}}
`
	w := loadWorkflow(t, yaml)
	t.Setenv(journal.EnvStateDir, t.TempDir())
	root := t.TempDir()
	e := &Engine{Workflow: w, Root: root, Timeout: 5 * time.Second}
	dir, err := journal.CreateRunDir(root, w.Workflow, "run1")
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
	for _, ev := range []journal.Event{
		{Kind: journal.KindRunStart, RunID: "run1"},
		{Kind: journal.KindStepEnter, RunID: "run1", Step: "a", Attempt: 1},
		{Kind: journal.KindTransition, RunID: "run1", Step: "a", Attempt: 1, Target: "b", Outcome: "success"},
	} {
		if _, err := log.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	log.Close()
	if _, err := e.Resume("run1", false); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if v := visitsOf(t, dir, "b"); v != 1 {
		t.Errorf("Visits[b] = %d, want 1 (its first and only entry)", v)
	}
}

// TestResume_BlockedInterventionStillCountsVisit: a BLOCKED resume is a
// fresh visit, unchanged.
func TestResume_BlockedInterventionStillCountsVisit(t *testing.T) {
	const yaml = `
workflow: blocked-visit
start: a
steps:
  - id: a
    kind: deterministic
    run: "true"
    postcondition: "false"
    attempts: 1
    max_visits: 5
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatal(err)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	before := visitsOf(t, dir, "a")
	if _, err := e.Resume("run1", false); err != nil {
		t.Fatal(err)
	}
	if after := visitsOf(t, dir, "a"); after != before+1 {
		t.Errorf("Visits[a] after blocked resume = %d, want %d (a blocked resume is a fresh visit)", after, before+1)
	}
}

// TestResume_CrashVisitsCountNotInflated: the extra-visit claim on its own,
// with headroom under max_visits:, so only the counter (not the cap) is in
// play: the resumed step sees ${visits} = 1 and replay agrees.
func TestResume_CrashVisitsCountNotInflated(t *testing.T) {
	const yaml = `
workflow: crash-visits-headroom
start: a
steps:
  - id: a
    kind: deterministic
    run: "echo ${visits} > seen.txt"
    max_visits: 5
    next: done
terminal: {done: {status: ok}}
`
	e, dir := crashedRun(t, yaml, "a")
	if _, err := e.Resume("run1", false); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(e.Root, "seen.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "1" {
		t.Errorf("${visits} = %q, want 1", got)
	}
	if v := visitsOf(t, dir, "a"); v != 1 {
		t.Errorf("Visits[a] = %d, want 1", v)
	}
}
