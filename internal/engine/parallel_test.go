package engine

import (
	"sort"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// TestParallel_TwoAgenticBranches_HappyPath: both branches agentic -> the
// engine returns DispatchParallel with both; the first Submit returns
// BranchRecorded (the other still pending) and journals only that branch's
// TRANSITION; the second Submit empties PendingBranches, fires routeParallel,
// and returns the real next instruction with the parallel step's own
// TRANSITION now journaled too.
func TestParallel_TwoAgenticBranches_HappyPath(t *testing.T) {
	const yaml = `
workflow: parallel-agentic
start: p
state:
  r1: {type: string, default: ""}
  r2: {type: string, default: ""}
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: join
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
  - id: join
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
	dp, ok := instr.(DispatchParallel)
	if !ok {
		t.Fatalf("got %T %+v, want DispatchParallel", instr, instr)
	}
	if dp.Step != "p" || len(dp.Agentic) != 2 {
		t.Fatalf("got %+v, want DispatchParallel for step p with 2 agentic branches", dp)
	}
	var steps []string
	for _, d := range dp.Agentic {
		steps = append(steps, d.Step)
	}
	sort.Strings(steps)
	if steps[0] != "b1" || steps[1] != "b2" {
		t.Fatalf("branch steps = %v, want [b1 b2]", steps)
	}

	// Cursor should stay parked on the parallel step while branches are
	// in flight.
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	rs := mustReplay(t, dir)
	if rs.Cursor.Step != "p" {
		t.Fatalf("Cursor.Step = %q, want %q", rs.Cursor.Step, "p")
	}

	// First branch reports: still waiting on the other.
	instr, err = e.Submit("run1", "b1", 1, []byte(`{"r1":"x"}`))
	if err != nil {
		t.Fatalf("Submit b1: %v", err)
	}
	br, ok := instr.(BranchRecorded)
	if !ok {
		t.Fatalf("got %T %+v, want BranchRecorded", instr, instr)
	}
	if br.ParallelStep != "p" || br.BranchStep != "b1" {
		t.Fatalf("got %+v, want ParallelStep p, BranchStep b1", br)
	}
	if len(br.Remaining) != 1 || br.Remaining[0] != "b2" {
		t.Fatalf("Remaining = %v, want [b2]", br.Remaining)
	}

	rs = mustReplay(t, dir)
	if rs.Cursor.Step != "p" {
		t.Fatalf("Cursor.Step = %q after one branch, want still %q", rs.Cursor.Step, "p")
	}
	if !sawGroupedTransition(t, dir, "b1") {
		t.Fatal("b1's TRANSITION not journaled")
	}
	if sawUngroupedTransition(t, dir, "p") {
		t.Fatal("parallel step p's own TRANSITION journaled too early")
	}

	// Second branch reports: the group resolves, routing continues.
	instr, err = e.Submit("run1", "b2", 1, []byte(`{"r2":"y"}`))
	if err != nil {
		t.Fatalf("Submit b2: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} via join", instr)
	}
	if !sawUngroupedTransition(t, dir, "p") {
		t.Fatal("parallel step p's own TRANSITION never journaled")
	}
}

// TestParallel_AllDeterministicBranches: the group resolves entirely
// in-process on Start, journaling both branch TRANSITIONs plus the
// parallel step's own, and returns the real next instruction directly.
func TestParallel_AllDeterministicBranches(t *testing.T) {
	const yaml = `
workflow: parallel-deterministic
start: p
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: join
  - id: b1
    kind: deterministic
    run: "echo hi"
  - id: b2
    kind: deterministic
    run: "echo bye"
  - id: join
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
		t.Fatalf("got %T %+v, want Terminal{ok} directly (no DispatchParallel)", instr, instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	if !sawGroupedTransition(t, dir, "b1") || !sawGroupedTransition(t, dir, "b2") {
		t.Fatal("both branch TRANSITIONs should be journaled")
	}
	if !sawUngroupedTransition(t, dir, "p") {
		t.Fatal("parallel step p's own TRANSITION should be journaled")
	}
}

// TestParallel_MixedBranches: the deterministic branch executes and
// transitions immediately; DispatchParallel.Agentic contains only the
// agentic one.
func TestParallel_MixedBranches(t *testing.T) {
	const yaml = `
workflow: parallel-mixed
start: p
state:
  r1: {type: string, default: ""}
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: join
  - id: b1
    kind: deterministic
    run: "echo hi"
  - id: b2
    kind: agentic
    description: "do b2"
    writes: {r1: {type: string}}
    postcondition: {all_set: [r1]}
  - id: join
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
	dp, ok := instr.(DispatchParallel)
	if !ok {
		t.Fatalf("got %T %+v, want DispatchParallel", instr, instr)
	}
	if len(dp.Agentic) != 1 || dp.Agentic[0].Step != "b2" {
		t.Fatalf("Agentic = %+v, want only b2", dp.Agentic)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	if !sawGroupedTransition(t, dir, "b1") {
		t.Fatal("deterministic branch b1 should already have transitioned")
	}
}

// TestParallel_AllOrNothingFailure: one branch's postcondition fails on
// submit; the engine still waits for the sibling (BranchRecorded, not an
// immediate failure). Once the sibling reports too (even a success), the
// GROUP outcome resolves to failure and routes via catch:.
func TestParallel_AllOrNothingFailure(t *testing.T) {
	const yaml = `
workflow: parallel-failure
start: p
state:
  r1: {type: string, default: ""}
  r2: {type: string, default: ""}
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: join
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
  - id: join
    kind: deterministic
    run: "echo done"
    next: done
  - id: recover
    kind: deterministic
    run: "echo recovered"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := instr.(DispatchParallel); !ok {
		t.Fatalf("got %T, want DispatchParallel", instr)
	}

	// b1's postcondition fails (r1 not set).
	instr, err = e.Submit("run1", "b1", 1, []byte(`{}`))
	if err != nil {
		t.Fatalf("Submit b1: %v", err)
	}
	if _, ok := instr.(BranchRecorded); !ok {
		t.Fatalf("got %T %+v, want BranchRecorded (still waiting on b2)", instr, instr)
	}

	// b2 succeeds, but the group still resolves to failure and routes via
	// catch: to recover.
	instr, err = e.Submit("run1", "b2", 1, []byte(`{"r2":"y"}`))
	if err != nil {
		t.Fatalf("Submit b2: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok} via recover", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	target := ungroupedTransitionTarget(t, dir, "p")
	if target != "recover" {
		t.Fatalf("parallel step's TRANSITION target = %q, want %q (routed via catch:)", target, "recover")
	}
}

// TestParallel_ResumeMidGroup: journal a parallel step entered, one branch
// transitioned, one branch entered but never transitioned (simulated
// crash) — resume produces DispatchParallel{Interrupted: true} containing
// only the un-transitioned branch.
func TestParallel_ResumeMidGroup(t *testing.T) {
	const yaml = `
workflow: parallel-resume
start: p
state:
  r1: {type: string, default: ""}
  r2: {type: string, default: ""}
steps:
  - id: p
    kind: parallel
    branches: [b1, b2]
    next: join
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
  - id: join
    kind: deterministic
    run: "echo done"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := e.Submit("run1", "b1", 1, []byte(`{"r1":"x"}`)); err != nil {
		t.Fatalf("Submit b1: %v", err)
	}

	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	dp, ok := instr.(DispatchParallel)
	if !ok {
		t.Fatalf("got %T %+v, want DispatchParallel", instr, instr)
	}
	if !dp.Interrupted {
		t.Fatal("want Interrupted: true on a resumed DispatchParallel")
	}
	if len(dp.Agentic) != 1 || dp.Agentic[0].Step != "b2" {
		t.Fatalf("Agentic = %+v, want only the still-outstanding b2", dp.Agentic)
	}
}

func mustReplay(t *testing.T, dir string) *journal.RunState {
	t.Helper()
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := journal.Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func sawGroupedTransition(t *testing.T, dir, step string) bool {
	t.Helper()
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == journal.KindTransition && ev.Step == step && ev.Group != "" {
			return true
		}
	}
	return false
}

func sawUngroupedTransition(t *testing.T, dir, step string) bool {
	t.Helper()
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == journal.KindTransition && ev.Step == step && ev.Group == "" {
			return true
		}
	}
	return false
}

func ungroupedTransitionTarget(t *testing.T, dir, step string) string {
	t.Helper()
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == journal.KindTransition && ev.Step == step && ev.Group == "" {
			return ev.Target
		}
	}
	return ""
}
