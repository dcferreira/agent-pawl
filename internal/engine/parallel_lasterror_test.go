package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

const parallelFailYAML = `
workflow: parallel-lasterror
start: p
state:
  r1: {type: string, default: ""}
  r2: {type: string, default: ""}
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: done
    catch:
      - on: failure
        next: recover
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
  - id: recover
    kind: deterministic
    run: "echo recovered"
    next: done
terminal: {done: {status: ok}}
`

func branchFailureText(t *testing.T, dir, parallelID, branch string) string {
	t.Helper()
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == journal.KindPostcondition && ev.Step == branch && !ev.OK {
			if ev.Group != parallelID {
				t.Fatalf("branch %s failure POSTCONDITION Group = %q, want %q", branch, ev.Group, parallelID)
			}
			return ev.Text
		}
	}
	t.Fatalf("no failed POSTCONDITION for branch %s", branch)
	return ""
}

// A failed branch's postcondition text never becomes the run-wide
// ${last_error} while the group is still in flight.
func TestParallel_BranchFailureDoesNotLeakIntoLastError(t *testing.T) {
	e := newTestEngine(t, parallelFailYAML)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit("run1", "b1", 1, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	if rs := mustReplay(t, dir); rs.LastError != "" {
		t.Fatalf("LastError mid-group = %q, want \"\" (branch result must stay branch-scoped)", rs.LastError)
	}
	// A sibling's pass must not matter either way.
	if _, err := e.Submit("run1", "b2", 1, []byte(`{"r2":"y"}`)); err != nil {
		t.Fatal(err)
	}
}

// A failed join sets ${last_error} to a summary of the failed branches, and
// a succeeding sibling does not clear it.
func TestParallel_FailedJoinSetsLastErrorSummary(t *testing.T) {
	e := newTestEngine(t, parallelFailYAML)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit("run1", "b1", 1, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit("run1", "b2", 1, []byte(`{"r2":"y"}`)); err != nil {
		t.Fatal(err)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	want := `branch "b1": ` + branchFailureText(t, dir, "p", "b1")
	if rs := mustReplay(t, dir); rs.LastError != want {
		t.Fatalf("LastError = %q, want %q", rs.LastError, want)
	}
	events, _ := journal.ReadEvents(dir)
	n := 0
	for _, ev := range events {
		if ev.Kind == journal.KindPostcondition && ev.Step == "p" && ev.Group == "" {
			n++
			if ev.OK || ev.Text != want {
				t.Errorf("summary event = %+v", ev)
			}
		}
	}
	if n != 1 {
		t.Fatalf("ungrouped POSTCONDITIONs for p = %d, want 1", n)
	}
}

// Deterministic branches: postcondition failure, hard failure with text and
// hard failure without text all appear in declaration order, and every
// branch-scoped POSTCONDITION is grouped.
func TestParallel_DeterministicBranchesSummaryAndGrouping(t *testing.T) {
	const yaml = `
workflow: parallel-det-lasterror
start: p
steps:
  - id: p
    kind: parallel
    branches: [lint, ok, test, quiet]
    outcomes:
      success: done
      failure: recover
  - id: lint
    kind: deterministic
    run: "echo hi"
    postcondition: {command: "echo lint-broken >&2; false"}
  - id: ok
    kind: deterministic
    run: "echo hi"
    postcondition: {command: "true"}
  - id: test
    kind: deterministic
    run: "echo boom >&2; exit 1"
  - id: quiet
    kind: deterministic
    run: "exit 1"
  - id: recover
    kind: deterministic
    run: "echo recovered"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal ok via recover", instr)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, _ := journal.ReadEvents(dir)
	for _, ev := range events {
		if ev.Kind == journal.KindPostcondition {
			switch ev.Step {
			case "lint", "ok", "test", "quiet":
				if ev.Group != "p" {
					t.Errorf("branch POSTCONDITION %+v not stamped Group p", ev)
				}
			}
		}
	}
	rs := mustReplay(t, dir)
	want := `branch "lint": ` + branchFailureText(t, dir, "p", "lint") +
		`; branch "test": ` + branchFailureText(t, dir, "p", "test") +
		`; branch "quiet" failed`
	if rs.LastError != want {
		t.Fatalf("LastError = %q, want %q", rs.LastError, want)
	}
	if strings.Contains(rs.LastError, `branch "ok"`) {
		t.Errorf("LastError names a passing branch: %q", rs.LastError)
	}
}

// Journaling the group summary twice (crash between the summary and the
// group's own TRANSITION, then resume) writes it once.
func TestParallel_GroupSummaryIsIdempotent(t *testing.T) {
	e := newTestEngine(t, parallelFailYAML)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatal(err)
	}
	// Both fail; the second submit closes the group and routes it.
	if _, err := e.Submit("run1", "b1", 1, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit("run1", "b2", 1, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, _ := journal.ReadEvents(dir)
	n := 0
	for _, ev := range events {
		if ev.Kind == journal.KindPostcondition && ev.Step == "p" && ev.Group == "" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("summary events = %d, want 1", n)
	}
	rs := mustReplay(t, dir)
	if !strings.Contains(rs.LastError, `branch "b1": `) || !strings.Contains(rs.LastError, `; branch "b2": `) {
		t.Fatalf("LastError = %q, want both branches in declaration order", rs.LastError)
	}
}

// Pins that the failed-group summary does not change a looped-back parallel
// step's attempt numbering: a failure edge is always viaCatch (resolveTarget),
// so the step's attempt_key budget is never cleared there, on main or here,
// and the second entry is attempt 2 either way.
func TestParallel_FailedGroupLoopAttemptsUnchangedBySummary(t *testing.T) {
	mark := t.TempDir() + "/mark"
	yaml := fmt.Sprintf(`
workflow: parallel-loop
start: p
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    attempts: 2
    attempt_key: "k"
    outcomes:
      success: done
      failure: fix
  - id: b1
    kind: deterministic
    run: "if [ -f %[1]s ]; then echo ok; else touch %[1]s; exit 1; fi"
  - id: b2
    kind: deterministic
    run: "echo ok"
  - id: fix
    kind: deterministic
    run: "echo fixing"
    next: p
terminal: {done: {status: ok}}
`, mark)
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal ok", instr)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, _ := journal.ReadEvents(dir)
	var attempts []int
	for _, ev := range events {
		if ev.Kind == journal.KindStepEnter && ev.Step == "p" && ev.Group == "" {
			attempts = append(attempts, ev.Attempt)
		}
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		for _, ev := range events {
			t.Logf("%s %s g=%q a=%d k=%q ok=%v sum=%v", ev.Kind, ev.Step, ev.Group, ev.Attempt, ev.AttemptKey, ev.OK, ev.Summary)
		}
		t.Fatalf("p STEP_ENTER attempts = %v, want [1 2] (unchanged from main)", attempts)
	}
}
