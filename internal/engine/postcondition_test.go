package engine

import (
	"testing"

	"github.com/dcferreira/agentic-workflow-fsm/internal/journal"
)

// TestPostcondition_ThreeForms exercises all three postcondition shapes
// (design/format-spec.md §D): command (spawned, exit 0 = pass), all_set
// (every named key non-empty), equals (rendered comparison).
func TestPostcondition_ThreeForms(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "command",
			yaml: `
workflow: pc-command
start: a
steps:
  - id: a
    kind: deterministic
    run: "true"
    postcondition: "test -n hello"
    next: done
terminal: {done: {status: ok}}
`,
		},
		{
			name: "all_set",
			yaml: `
workflow: pc-allset
start: a
state:
  branch: {type: string, default: "feat/x"}
steps:
  - id: a
    kind: deterministic
    run: "true"
    postcondition: {all_set: [branch]}
    next: done
terminal: {done: {status: ok}}
`,
		},
		{
			name: "equals",
			yaml: `
workflow: pc-equals
start: a
state:
  status: {type: string, default: "ready"}
steps:
  - id: a
    kind: deterministic
    run: "true"
    postcondition: {equals: {status: "ready"}}
    next: done
terminal: {done: {status: ok}}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine(t, tc.yaml)
			instr, err := e.Start("run1", nil)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			term, ok := instr.(Terminal)
			if !ok || term.Status != "ok" {
				t.Fatalf("got %+v, want Terminal{ok}", instr)
			}
		})
	}
}

// TestPostcondition_SoftStillGatesAndRetries: soft: true is bookkeeping
// only — it still executes and still gates the transition
// (design/format-spec.md §B.7).
func TestPostcondition_SoftStillGatesAndRetries(t *testing.T) {
	const yaml = `
workflow: soft-gates
start: a
steps:
  - id: a
    kind: deterministic
    run: "true"
    postcondition: {command: "false", soft: true}
    attempts: 2
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
		t.Fatalf("got %+v, want Terminal{blocked}: a soft postcondition still gates and still exhausts attempts", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawSoftFailure bool
	for _, ev := range events {
		if ev.Kind == journal.KindPostcondition && !ev.OK {
			if !ev.Soft {
				t.Errorf("POSTCONDITION.Soft = false, want true")
			}
			sawSoftFailure = true
		}
	}
	if !sawSoftFailure {
		t.Fatal("no failing POSTCONDITION recorded")
	}
}
