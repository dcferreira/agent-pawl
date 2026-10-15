package engine

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

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

// TestResume_ComparesRecomputedDigestNotStoredOne: plan.json's recorded
// digest is only a record; Resume re-digests plan.Workflow with the running
// code and compares that to the current workflow's digest. So a plan whose
// stored digest string is stale (e.g. written by a build whose digest
// function differed) still resumes when the workflow itself is unchanged.
func TestResume_ComparesRecomputedDigestNotStoredOne(t *testing.T) {
	const yaml = `
workflow: digest-recompute
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
	planPath := filepath.Join(journal.RunDir(e.Root, e.Workflow.Workflow, "run1"), "plan.json")
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`"digest": "[0-9a-f]+"`)
	if !re.Match(data) {
		t.Fatalf("plan.json has no digest field: %s", data)
	}
	data = re.ReplaceAll(data, []byte(`"digest": "recorded-by-another-build"`))
	if err := os.WriteFile(planPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Resume("run1", false); errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Resume compared the stored digest string: %v", err)
	} else if err != nil {
		t.Fatalf("Resume: %v", err)
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

const humanCrashWorkflow = `
workflow: human-crash
start: ask
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options: [alice, bob]
    timeout: 10s
    outcomes:
      alice: done
      bob: done
      timeout: blocked
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`

func countEvents(t *testing.T, dir string, kind journal.Kind, step string) int {
	t.Helper()
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range events {
		if ev.Kind == kind && ev.Step == step {
			n++
		}
	}
	return n
}

// TestResume_HumanCrashResumeKeepsOriginalDeadline: a crash resume of a
// parked human step re-prints the same Ask and journals only RESUME events
// (no new STEP_ENTER / HUMAN_ASKED), so the timeout: clock, anchored on the
// HUMAN_ASKED, stays the original one: an answer after it routes timeout.
func TestResume_HumanCrashResumeKeepsOriginalDeadline(t *testing.T) {
	e := newTestEngine(t, humanCrashWorkflow)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatal(err)
	}
	orig := instr.(Ask)
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	backdateJournal(t, e, "run1", 11*time.Second) // original HUMAN_ASKED is past the 10s timeout

	for i := 0; i < 3; i++ {
		instr, err = e.Resume("run1", false)
		if err != nil {
			t.Fatalf("Resume %d: %v", i, err)
		}
		if got, ok := instr.(Ask); !ok || !reflect.DeepEqual(got, orig) {
			t.Fatalf("Resume %d returned %#v, want the original %#v", i, instr, orig)
		}
	}
	if n := countEvents(t, dir, journal.KindStepEnter, "ask"); n != 1 {
		t.Errorf("STEP_ENTER count = %d, want 1", n)
	}
	if n := countEvents(t, dir, journal.KindHumanAsked, "ask"); n != 1 {
		t.Errorf("HUMAN_ASKED count = %d, want 1", n)
	}
	if n := countEvents(t, dir, journal.KindResume, "ask"); n != 3 {
		t.Errorf("RESUME count = %d, want 3", n)
	}

	instr, err = e.SubmitHuman("run1", "ask", orig.Attempt, []byte(`{"selected":["alice"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Outcome != "timeout" {
		t.Fatalf("got %#v, want a timeout Terminal (the original deadline holds across crash resumes)", instr)
	}
}

// TestResume_HumanBlockedResumeGetsFreshDeadline: a blocked (intervention)
// resume is a fresh visit: it re-asks and re-anchors the deadline, so an
// answer past the original window but inside the new one is accepted.
func TestResume_HumanBlockedResumeGetsFreshDeadline(t *testing.T) {
	e := newTestEngine(t, humanCrashWorkflow)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := instr.(Ask)
	backdateJournal(t, e, "run1", 11*time.Second)
	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["alice"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("got %#v, want blocked (timeout: blocked)", instr)
	}

	instr, err = e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	a2, ok := instr.(Ask)
	if !ok {
		t.Fatalf("got %#v, want a fresh Ask", instr)
	}
	instr, err = e.SubmitHuman("run1", "ask", a2.Attempt, []byte(`{"selected":["alice"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %#v, want Terminal{ok}: the blocked resume must grant a fresh window", instr)
	}
}

// TestResume_HumanOptionsFromReprintsSameOptions: crash-resuming an
// options_from: human step re-prints exactly the original options.
func TestResume_HumanOptionsFromReprintsSameOptions(t *testing.T) {
	const yaml = `
workflow: human-options-from
start: ask
state:
  opts: {type: json, default: ["x", "y", "z"]}
  pick: {type: string, default: ""}
steps:
  - id: ask
    kind: human
    question: "Pick one"
    options_from: opts
    writes: [pick]
    timeout: 10s
    outcomes: {chosen: done, timeout: done}
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatal(err)
	}
	orig := instr.(Ask)
	if !reflect.DeepEqual(orig.Options, []string{"x", "y", "z"}) {
		t.Fatalf("setup: options = %v", orig.Options)
	}
	for i := 0; i < 2; i++ {
		instr, err = e.Resume("run1", false)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := instr.(Ask); !ok || !reflect.DeepEqual(got, orig) {
			t.Fatalf("Resume %d returned %#v, want %#v", i, instr, orig)
		}
	}
}

// TestResume_HumanCrashBeforeHumanAsked: a crash between the human step's
// STEP_ENTER and its HUMAN_ASKED asks exactly once on resume; a further
// crash resume then appends no further HUMAN_ASKED.
func TestResume_HumanCrashBeforeHumanAsked(t *testing.T) {
	e, dir := crashedRun(t, humanCrashWorkflow, "ask")
	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := instr.(Ask); !ok {
		t.Fatalf("got %#v, want Ask", instr)
	}
	if n := countEvents(t, dir, journal.KindHumanAsked, "ask"); n != 1 {
		t.Fatalf("HUMAN_ASKED count after first resume = %d, want 1", n)
	}
	if _, err := e.Resume("run1", false); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, dir, journal.KindHumanAsked, "ask"); n != 1 {
		t.Errorf("HUMAN_ASKED count after second crash resume = %d, want 1", n)
	}
	if v := visitsOf(t, dir, "ask"); v != 1 {
		t.Errorf("Visits[ask] = %d, want 1", v)
	}
}

// TestResume_CrashNeverConsumesVisitOrAttempt pins the rule "a crash resume
// never consumes a visit or an attempt", for every kind that can be in
// flight: with max_visits: 2, five crash resumes followed by a normal
// completion never route exhausted, and neither Visits nor the cursor's
// attempt moves because of the resumes.
func TestResume_CrashNeverConsumesVisitOrAttempt(t *testing.T) {
	const crashes = 5
	type kase struct {
		name  string
		step  string
		setup func(t *testing.T) (*Engine, string)
		// finish completes the step normally and returns the final instruction.
		finish func(t *testing.T, e *Engine) Instruction
	}
	waitYAML := `
workflow: pin-wait
start: w
steps:
  - id: w
    kind: wait
    poll: "echo PASSED"
    every: 1s
    timeout: 10s
    emits: pairs
    max_visits: 2
    outcomes: {PASSED: done, timeout: done}
terminal: {done: {status: ok}}
`
	start := func(yaml string) func(t *testing.T) (*Engine, string) {
		return func(t *testing.T) (*Engine, string) {
			e := newTestEngine(t, yaml)
			if _, err := e.Start("run1", nil); err != nil {
				t.Fatal(err)
			}
			return e, journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
		}
	}
	fe := strings.Replace(feAgentic(`["a","b"]`, agBody, "", ""),
		"  - id: fan\n    kind: parallel\n", "  - id: fan\n    kind: parallel\n    max_visits: 2\n", 1)
	cases := []kase{
		{
			name: "human", step: "ask",
			setup: start(strings.Replace(humanCrashWorkflow, "    timeout: 10s\n", "    timeout: 10s\n    max_visits: 2\n", 1)),
			finish: func(t *testing.T, e *Engine) Instruction {
				instr, err := e.SubmitHuman("run1", "ask", 1, []byte(`{"selected":["alice"]}`))
				if err != nil {
					t.Fatal(err)
				}
				return instr
			},
		},
		{
			name: "agentic", step: "a",
			setup: start(`
workflow: pin-agentic
start: a
state:
  result: {type: string, default: ""}
steps:
  - id: a
    kind: agentic
    description: "do it"
    writes: {result: {type: string}}
    postcondition: {all_set: [result]}
    max_visits: 2
    next: done
terminal: {done: {status: ok}}
`),
			finish: func(t *testing.T, e *Engine) Instruction {
				instr, err := e.Submit("run1", "a", 1, []byte(`{"result":"x"}`))
				if err != nil {
					t.Fatal(err)
				}
				return instr
			},
		},
		{
			// A deterministic step completes on its first resume, so the
			// earlier crashes are simulated by journaling RESUMEs directly
			// after the crashed STEP_ENTER; the final Resume is the last.
			name: "deterministic", step: "a",
			setup: func(t *testing.T) (*Engine, string) {
				e, dir := crashedRun(t, `
workflow: pin-det
start: a
steps:
  - id: a
    kind: deterministic
    run: "true"
    max_visits: 2
    next: done
terminal: {done: {status: ok}}
`, "a")
				log, err := journal.OpenLog(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer log.Close()
				for i := 0; i < crashes-1; i++ {
					if _, err := log.Append(journal.Event{Kind: journal.KindResume, RunID: "run1", Step: "a", Attempt: 1}); err != nil {
						t.Fatal(err)
					}
				}
				return e, dir
			},
			finish: func(t *testing.T, e *Engine) Instruction {
				instr, err := e.Resume("run1", false)
				if err != nil {
					t.Fatal(err)
				}
				return instr
			},
		},
		{
			name: "parallel", step: "p",
			setup: start(`
workflow: pin-parallel
start: p
state:
  r1: {type: string, default: ""}
  r2: {type: string, default: ""}
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    max_visits: 2
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
`),
			finish: func(t *testing.T, e *Engine) Instruction {
				if _, err := e.Submit("run1", "b1", 1, []byte(`{"r1":"x"}`)); err != nil {
					t.Fatal(err)
				}
				instr, err := e.Submit("run1", "b2", 1, []byte(`{"r2":"y"}`))
				if err != nil {
					t.Fatal(err)
				}
				return instr
			},
		},
		{
			name: "parallel foreach", step: "fan",
			setup: func(t *testing.T) (*Engine, string) {
				e, _, dir := agStart(t, fe)
				return e, dir
			},
			finish: func(t *testing.T, e *Engine) Instruction {
				var instr Instruction
				for i, r := range []string{"A", "B"} {
					var err error
					instr, err = e.SubmitItem("run1", "one", i, 1, []byte(`{"res":"`+r+`"}`))
					if err != nil {
						t.Fatal(err)
					}
				}
				return instr
			},
		},
		{name: "wait", step: "w", setup: start(waitYAML),
			finish: func(t *testing.T, e *Engine) Instruction {
				newFakeClock().install(e)
				instr, err := e.Poll("run1", "w", func(PollIteration) {})
				if err != nil {
					t.Fatal(err)
				}
				return instr
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, dir := c.setup(t)
			before := mustReplay(t, dir)
			for i := 0; i < crashes; i++ {
				if c.name == "deterministic" {
					break // the final Resume in finish is the crashes-th
				}
				if _, err := e.Resume("run1", false); err != nil {
					t.Fatalf("Resume %d: %v", i, err)
				}
			}
			after := mustReplay(t, dir)
			if after.Visits[c.step] != 1 && c.name != "deterministic" {
				t.Errorf("Visits[%s] after %d crash resumes = %d, want 1", c.step, crashes, after.Visits[c.step])
			}
			if after.Cursor.Attempt != before.Cursor.Attempt {
				t.Errorf("cursor attempt %d -> %d across crash resumes, want unchanged", before.Cursor.Attempt, after.Cursor.Attempt)
			}
			instr := c.finish(t, e)
			if term, ok := instr.(Terminal); !ok || term.Status != "ok" || term.Outcome == "exhausted" {
				t.Fatalf("got %#v, want Terminal{ok}, never exhausted", instr)
			}
			if v := mustReplay(t, dir).Visits[c.step]; v != 1 {
				t.Errorf("Visits[%s] at the end = %d, want 1", c.step, v)
			}
		})
	}
}

// journalRun hand-builds a run whose journal is exactly RUN_START plus evs
// (crashedRun appends one fixed STEP_ENTER; these scenarios need a longer
// history), and returns an engine ready for Resume.
func journalRun(t *testing.T, yaml string, evs ...journal.Event) (*Engine, string) {
	t.Helper()
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
	defer log.Close()
	for _, ev := range append([]journal.Event{{Kind: journal.KindRunStart, RunID: "run1"}}, evs...) {
		if _, err := log.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	return e, dir
}

// TestResume_HumanCrashAfterInterventionResumeReasksFresh: a blocked run
// whose intervention RESUME is durable but whose fresh STEP_ENTER/HUMAN_ASKED
// is not (crash in between). The next plain crash resume must enter the step
// and ask afresh, not re-print against the previous visit's stale HUMAN_ASKED.
func TestResume_HumanCrashAfterInterventionResumeReasksFresh(t *testing.T) {
	e := newTestEngine(t, humanCrashWorkflow)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := instr.(Ask)
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	backdateJournal(t, e, "run1", 11*time.Second)
	if _, err := e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["alice"]}`)); err != nil {
		t.Fatal(err)
	}
	log, err := journal.OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(journal.Event{Kind: journal.KindResume, RunID: "run1", Step: "ask", Attempt: 1, Intervention: true}); err != nil {
		t.Fatal(err)
	}
	log.Close()

	instr, err = e.Resume("run1", false) // plain crash resume
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	a2, ok := instr.(Ask)
	if !ok {
		t.Fatalf("got %#v, want Ask", instr)
	}
	if n := countEvents(t, dir, journal.KindStepEnter, "ask"); n != 2 {
		t.Errorf("STEP_ENTER count = %d, want 2 (one fresh)", n)
	}
	if n := countEvents(t, dir, journal.KindHumanAsked, "ask"); n != 2 {
		t.Errorf("HUMAN_ASKED count = %d, want 2 (one fresh)", n)
	}
	instr, err = e.SubmitHuman("run1", "ask", a2.Attempt, []byte(`{"selected":["alice"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %#v, want Terminal{ok}: the new window must be accepted", instr)
	}
}

const loopTail = `
  - id: x
    kind: deterministic
    run: "true"
    next: %s
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`

// loopEvents is the journal of a run that left `step` via alice, ran x, and
// was transitioned back into `step` -- then crashed before step's second
// STEP_ENTER. Every event is later backdated past step's 10s timeout.
func loopEvents(step string, askedEvents ...journal.Event) []journal.Event {
	evs := []journal.Event{{Kind: journal.KindStepEnter, RunID: "run1", Step: step, Attempt: 1}}
	evs = append(evs, askedEvents...)
	return append(evs,
		journal.Event{Kind: journal.KindTransition, RunID: "run1", Step: step, Attempt: 1, Target: "x", Outcome: "alice"},
		journal.Event{Kind: journal.KindStepEnter, RunID: "run1", Step: "x", Attempt: 1},
		journal.Event{Kind: journal.KindTransition, RunID: "run1", Step: "x", Attempt: 1, Target: step, Outcome: "success"},
	)
}

// TestResume_HumanLoopCrashBeforeSecondEnterAsksFresh: human -> x -> human;
// crash after x's TRANSITION into ask, before ask's second STEP_ENTER. The
// crash resume is a first entry of visit 2 and must ask afresh.
func TestResume_HumanLoopCrashBeforeSecondEnterAsksFresh(t *testing.T) {
	yaml := `
workflow: human-loop
start: ask
steps:
  - id: ask
    kind: human
    question: "Again?"
    options: [alice, bob]
    timeout: 10s
    max_visits: 3
    outcomes: {alice: x, bob: done, timeout: blocked}` + strings.Replace(loopTail, "%s", "ask", 1)
	e, dir := journalRun(t, yaml, loopEvents("ask", journal.Event{Kind: journal.KindHumanAsked, RunID: "run1", Step: "ask", Attempt: 1})...)
	backdateJournal(t, e, "run1", 11*time.Second)

	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	a, ok := instr.(Ask)
	if !ok {
		t.Fatalf("got %#v, want Ask", instr)
	}
	if n := countEvents(t, dir, journal.KindStepEnter, "ask"); n != 2 {
		t.Errorf("STEP_ENTER count = %d, want 2 (one fresh)", n)
	}
	if n := countEvents(t, dir, journal.KindHumanAsked, "ask"); n != 2 {
		t.Errorf("HUMAN_ASKED count = %d, want 2 (one fresh)", n)
	}
	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["bob"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %#v, want Terminal{ok}: visit 2's window is new", instr)
	}
}

// TestResume_WaitLoopCrashBeforeSecondEnterIsFreshVisit: the same loop shape
// for wait. Resume must journal the second visit's STEP_ENTER (so it is
// counted and the poll deadline anchors on it), not re-print against visit 1.
func TestResume_WaitLoopCrashBeforeSecondEnterIsFreshVisit(t *testing.T) {
	yaml := `
workflow: wait-loop
start: w
steps:
  - id: w
    kind: wait
    poll: cat status
    every: 1s
    timeout: 10s
    emits: pairs
    max_visits: 3
    outcomes: {PASSED: x, timeout: blocked}` + strings.Replace(loopTail, "%s", "w", 1)
	evs := []journal.Event{
		{Kind: journal.KindStepEnter, RunID: "run1", Step: "w", Attempt: 1},
		{Kind: journal.KindTransition, RunID: "run1", Step: "w", Attempt: 1, Target: "x", Outcome: "PASSED"},
		{Kind: journal.KindStepEnter, RunID: "run1", Step: "x", Attempt: 1},
		{Kind: journal.KindTransition, RunID: "run1", Step: "x", Attempt: 1, Target: "w", Outcome: "success"},
	}
	e, dir := journalRun(t, yaml, evs...)
	backdateJournal(t, e, "run1", 11*time.Second)
	newFakeClock().install(e)
	writeStatus(t, e, "PENDING")

	if _, err := e.Resume("run1", false); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if n := countEvents(t, dir, journal.KindStepEnter, "w"); n != 2 {
		t.Errorf("STEP_ENTER count = %d, want 2 (visit 2 journaled)", n)
	}
	if v := visitsOf(t, dir, "w"); v != 2 {
		t.Errorf("Visits[w] = %d, want 2", v)
	}
	var iters []PollIteration
	if _, err := e.Poll("run1", "w", func(it PollIteration) { iters = append(iters, it) }); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(iters) == 0 || iters[0].TimedOut {
		t.Errorf("first poll iteration timed out against visit 1's deadline: %+v", iters)
	}
}

// TestResume_CrashBeforeStepEnterIsAFreshEntry pins the other half of "a
// crash resume never consumes a visit or an attempt": a crash BEFORE a step's
// own STEP_ENTER (the journal ends at the predecessor's TRANSITION into it) is
// not a crash re-entry of an in-flight step. The resume must journal the
// step's STEP_ENTER (so the visit is counted), must not flag it as
// interrupted/re-entered, and must start a fresh deadline where the kind has
// one. Each kind is run both as a first visit (i) and as a loop re-entry
// (ii), where the old visit's events (e.g. its HUMAN_ASKED) are still in the
// journal and must not be mistaken for this one's.
func TestResume_CrashBeforeStepEnterIsAFreshEntry(t *testing.T) {
	const tail = `
  - id: x
    kind: deterministic
    run: "true"
    next: s
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	head := func(body string) string {
		return `
workflow: fresh-entry
start: pre
state:
  xs: {type: json, default: ["a", "b"]}
  out: {type: json, default: []}
  res: {type: string, default: ""}
steps:
  - id: pre
    kind: deterministic
    run: "true"
    next: s
` + body + tail
	}
	type kase struct {
		name string
		yaml string
		// visit1 is the step's own journal from its first STEP_ENTER up to
		// (excluding) the TRANSITION out, ungrouped events only.
		visit1  []journal.Event
		outcome string // outcome of the TRANSITION s -> x in visit 1
		// check runs after the resume (journal already backdated before it).
		check func(t *testing.T, e *Engine, instr Instruction)
	}
	enter := journal.Event{Kind: journal.KindStepEnter, RunID: "run1", Step: "s", Attempt: 1}
	asked := journal.Event{Kind: journal.KindHumanAsked, RunID: "run1", Step: "s", Attempt: 1}
	notInterrupted := func(t *testing.T, instr Instruction) {
		t.Helper()
		switch v := instr.(type) {
		case Dispatch:
			if v.Interrupted {
				t.Errorf("Dispatch.Interrupted = true for a first entry: %+v", v)
			}
		case DispatchParallel:
			if v.Interrupted {
				t.Errorf("DispatchParallel.Interrupted = true for a first entry: %+v", v)
			}
		}
	}
	cases := []kase{
		{
			name: "human",
			yaml: head(`  - id: s
    kind: human
    question: "ok?"
    options: [alice, bob]
    timeout: 10s
    max_visits: 3
    outcomes: {alice: x, bob: done, timeout: blocked}`),
			visit1: []journal.Event{enter, asked}, outcome: "alice",
			check: func(t *testing.T, e *Engine, instr Instruction) {
				a, ok := instr.(Ask)
				if !ok {
					t.Fatalf("got %#v, want Ask", instr)
				}
				out, err := e.SubmitHuman("run1", "s", a.Attempt, []byte(`{"selected":["bob"]}`))
				if err != nil {
					t.Fatal(err)
				}
				if term, ok := out.(Terminal); !ok || term.Status != "ok" {
					t.Fatalf("got %#v, want Terminal{ok}: the new window must be accepted", out)
				}
			},
		},
		{
			name: "agentic",
			yaml: head(`  - id: s
    kind: agentic
    description: "do it"
    writes: {out: {type: json}}
    postcondition: "true"
    max_visits: 3
    next: x`),
			visit1: []journal.Event{enter}, outcome: "success",
			check: func(t *testing.T, e *Engine, instr Instruction) {
				if _, ok := instr.(Dispatch); !ok {
					t.Fatalf("got %#v, want Dispatch", instr)
				}
				notInterrupted(t, instr)
			},
		},
		{
			name: "deterministic",
			yaml: head(`  - id: s
    kind: deterministic
    run: "echo STOP"
    emits: pairs
    max_visits: 3
    outcomes: {STOP: done, GO: x}`),
			visit1: []journal.Event{enter}, outcome: "GO",
			check: func(t *testing.T, e *Engine, instr Instruction) {
				if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
					t.Fatalf("got %#v, want Terminal{ok}", instr)
				}
			},
		},
		{
			name: "parallel",
			yaml: head(`  - id: s
    kind: parallel
    branches: [b1, b2]
    max_visits: 3
    next: x
  - id: b1
    kind: agentic
    description: "b1"
    writes: {out: {type: json}}
    postcondition: "true"
  - id: b2
    kind: agentic
    description: "b2"
    writes: {out: {type: json}}
    postcondition: "true"`),
			visit1: []journal.Event{enter}, outcome: "success",
			check: func(t *testing.T, e *Engine, instr Instruction) {
				if _, ok := instr.(DispatchParallel); !ok {
					t.Fatalf("got %#v, want DispatchParallel", instr)
				}
				notInterrupted(t, instr)
			},
		},
		{
			name: "parallel foreach",
			yaml: head(`  - id: s
    kind: parallel
    foreach: {over: xs, body: one, collect: out}
    max_visits: 3
    outcomes: {success: x, partial: done, failure: done}
  - id: one
    kind: agentic
    description: "item ${item}"
    writes: {res: {type: string}}
    postcondition: "true"`),
			visit1: []journal.Event{enter}, outcome: "success",
			check: func(t *testing.T, e *Engine, instr Instruction) {
				if _, ok := instr.(DispatchParallel); !ok {
					t.Fatalf("got %#v, want DispatchParallel", instr)
				}
				notInterrupted(t, instr)
			},
		},
		{
			name: "wait",
			yaml: head(`  - id: s
    kind: wait
    poll: cat status
    every: 1s
    timeout: 10s
    emits: pairs
    max_visits: 3
    outcomes: {PASSED: x, timeout: blocked}`),
			visit1: []journal.Event{enter}, outcome: "PASSED",
			check: func(t *testing.T, e *Engine, instr Instruction) {
				if _, ok := instr.(Wait); !ok {
					t.Fatalf("got %#v, want Wait", instr)
				}
				var iters []PollIteration
				if _, err := e.Poll("run1", "s", func(it PollIteration) { iters = append(iters, it) }); err != nil {
					t.Fatalf("Poll: %v", err)
				}
				if len(iters) == 0 || iters[0].TimedOut {
					t.Errorf("first poll iteration timed out against an old visit's deadline: %+v", iters)
				}
			},
		},
	}
	pre := []journal.Event{
		{Kind: journal.KindStepEnter, RunID: "run1", Step: "pre", Attempt: 1},
		{Kind: journal.KindTransition, RunID: "run1", Step: "pre", Attempt: 1, Target: "s", Outcome: "success"},
	}
	for _, c := range cases {
		for _, loop := range []bool{false, true} {
			name := c.name + "/first-visit"
			wantVisits := 1
			if loop {
				name = c.name + "/loop-reentry"
				wantVisits = 2
			}
			t.Run(name, func(t *testing.T) {
				evs := append([]journal.Event{}, pre...)
				if loop {
					evs = append(evs, c.visit1...)
					evs = append(evs,
						journal.Event{Kind: journal.KindTransition, RunID: "run1", Step: "s", Attempt: 1, Target: "x", Outcome: c.outcome},
						journal.Event{Kind: journal.KindStepEnter, RunID: "run1", Step: "x", Attempt: 1},
						journal.Event{Kind: journal.KindTransition, RunID: "run1", Step: "x", Attempt: 1, Target: "s", Outcome: "success"},
					)
				}
				e, dir := journalRun(t, c.yaml, evs...)
				backdateJournal(t, e, "run1", 11*time.Second)
				newFakeClock().install(e)
				writeStatus(t, e, "PENDING")

				instr, err := e.Resume("run1", false)
				if err != nil {
					t.Fatalf("Resume: %v", err)
				}
				if n := countEvents(t, dir, journal.KindStepEnter, "s"); n != wantVisits {
					t.Errorf("STEP_ENTER count for s = %d, want %d (this entry journaled)", n, wantVisits)
				}
				rs := mustReplay(t, dir)
				if rs.Visits["s"] != wantVisits {
					t.Errorf("Visits[s] = %d, want %d", rs.Visits["s"], wantVisits)
				}
				if rs.ResumedInFlight != "" {
					t.Errorf("ResumedInFlight = %q after the step's STEP_ENTER, want empty (not a crash re-entry)", rs.ResumedInFlight)
				}
				c.check(t, e, instr)
			})
		}
	}
}

// TestResume_CrashAfterInterventionKeepsInterrupted: a blocked run whose
// intervention RESUME is durable but whose fresh STEP_ENTER is not (crash in
// between). The next plain crash resume is no longer a blocked resume, but a
// person did intervene after the block, so the agentic and parallel
// dispatches must still carry Interrupted.
func TestResume_CrashAfterInterventionKeepsInterrupted(t *testing.T) {
	const head = `
workflow: intervene
start: s
state:
  out: {type: json, default: []}
steps:
`
	const tail = `
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	cases := []struct {
		name, yaml string
	}{
		{"agentic", head + `  - id: s
    kind: agentic
    description: "do it"
    writes: {out: {type: json}}
    postcondition: "true"
    next: done` + tail},
		{"parallel", head + `  - id: s
    kind: parallel
    branches: [b1, b2]
    next: done
  - id: b1
    kind: agentic
    description: "b1"
    writes: {out: {type: json}}
    postcondition: "true"
  - id: b2
    kind: agentic
    description: "b2"
    writes: {out: {type: json}}
    postcondition: "true"` + tail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := journalRun(t, c.yaml,
				journal.Event{Kind: journal.KindStepEnter, RunID: "run1", Step: "s", Attempt: 1},
				journal.Event{Kind: journal.KindRunEnd, RunID: "run1", Step: "s", Status: "blocked", Reason: "s: failure"},
				journal.Event{Kind: journal.KindResume, RunID: "run1", Step: "s", Attempt: 1, Intervention: true},
			)
			instr, err := e.Resume("run1", false) // plain crash resume
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			switch v := instr.(type) {
			case Dispatch:
				if !v.Interrupted {
					t.Errorf("Dispatch.Interrupted = false after an intervention: %+v", v)
				}
			case DispatchParallel:
				// dispatchParallel carries the flag on each branch Dispatch.
				if len(v.Agentic) == 0 {
					t.Fatalf("no branch dispatches: %+v", v)
				}
				for _, d := range v.Agentic {
					if !d.Interrupted {
						t.Errorf("branch %s Interrupted = false after an intervention", d.Step)
					}
				}
			default:
				t.Fatalf("got %#v, want a dispatch", instr)
			}
		})
	}
}

// TestSubmitHuman_AnswerWrittenBeforeCrashSurvivesDeadline: SubmitHuman
// accepted an answer in time and journaled its WRITES, then crashed before the
// TRANSITION. After the deadline passes, the crash resume re-asks and the
// re-submitted answer must route its own outcome, not timeout (§B.5: nothing
// is written on timeout).
func TestSubmitHuman_AnswerWrittenBeforeCrashSurvivesDeadline(t *testing.T) {
	const yaml = `
workflow: human-writes-crash
start: ask
state:
  who: {type: string, default: ""}
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options: [alice, bob]
    timeout: 10s
    writes: {who: {type: string}}
    outcomes:
      alice: done
      bob: done
      timeout: blocked
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	e, _ := journalRun(t, yaml,
		journal.Event{Kind: journal.KindStepEnter, RunID: "run1", Step: "ask", Attempt: 1},
		journal.Event{Kind: journal.KindHumanAsked, RunID: "run1", Step: "ask", Attempt: 1},
		journal.Event{Kind: journal.KindWrites, RunID: "run1", Step: "ask", Attempt: 1, Writes: map[string]any{"who": "alice"}},
	)
	backdateJournal(t, e, "run1", 11*time.Second)
	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	a, ok := instr.(Ask)
	if !ok {
		t.Fatalf("got %#v, want Ask", instr)
	}
	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["alice"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %#v, want Terminal{ok}: the in-time answer was already written", instr)
	}
}
