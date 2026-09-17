package engine

import (
	"testing"

	"github.com/dcferreira/agentic-workflow-fsm/internal/journal"
	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// TestSubmit_RetriesOnPostconditionFailure exercises the agentic
// attempt-retry path end to end: a failing postcondition redispatches the
// same step with the previous failure text attached, up to attempts:.
func TestSubmit_RetriesOnPostconditionFailure(t *testing.T) {
	const yaml = `
workflow: agentic-retry
start: work
state:
  findings: {type: json, default: []}
steps:
  - id: work
    kind: agentic
    description: "fix ${findings}"
    writes: {findings: {type: json}}
    postcondition: {all_set: [findings]}
    attempts: 3
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	d, ok := instr.(Dispatch)
	if !ok || d.Attempt != 1 || d.PreviousFailure != "" {
		t.Fatalf("got %+v, want fresh Dispatch attempt 1 with no previous failure", instr)
	}

	// Empty json ("null") fails all_set: findings.
	instr, err = e.Submit("run1", "work", 1, []byte(`{"findings": null}`))
	if err != nil {
		t.Fatalf("Submit attempt 1: %v", err)
	}
	d, ok = instr.(Dispatch)
	if !ok || d.Attempt != 2 {
		t.Fatalf("got %+v, want Dispatch attempt 2 after postcondition failure", instr)
	}
	if d.PreviousFailure == "" {
		t.Error("PreviousFailure should carry the prior attempt's postcondition failure text")
	}

	// A non-empty value passes all_set: this time.
	instr, err = e.Submit("run1", "work", 2, []byte(`{"findings": [1,2]}`))
	if err != nil {
		t.Fatalf("Submit attempt 2: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}", instr)
	}
}

// TestSubmit_UndeclaredKeyDiscarded: "prose outside the schema is journalled
// and discarded" (design/format-spec.md §B.6) — an extra key in the
// returned JSON is not an error.
func TestSubmit_UndeclaredKeyDiscarded(t *testing.T) {
	const yaml = `
workflow: agentic-extra-key
start: work
state:
  result: {type: string, default: ""}
steps:
  - id: work
    kind: agentic
    description: "do it"
    writes: {result: {type: string}}
    postcondition: "true"
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	instr, err := e.Submit("run1", "work", 1, []byte(`{"result":"x","extra_prose":"ignored please"}`))
	if err != nil {
		t.Fatalf("Submit with an extra undeclared key: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}", instr)
	}
}

// TestSubmit_EmptyWritesSchemaFailsClosed is the "five things" item 4 test:
// a Workflow reaching this package with no typed writes: schema on an
// agentic step must never accept a returned key.
func TestSubmit_EmptyWritesSchemaFailsClosed(t *testing.T) {
	// spec.Validate rejects an agentic step with no typed writes: map, so
	// this build's real entry point (wf run, gated on Validate) can never
	// hand the engine a Workflow like this. The brief still asks for
	// fail-closed behaviour if one ever does reach this package, so this
	// test builds the Workflow value directly rather than through
	// spec.Load/Validate.
	w := &spec.Workflow{
		Workflow: "no-schema",
		Start:    "work",
		MaxSteps: 200,
		Steps: []spec.Step{{
			ID:            "work",
			Kind:          "agentic",
			Description:   "do it",
			Postcondition: &spec.Postcondition{Command: "true", HasCommand: true},
			Attempts:      1,
			MaxVisits:     10,
			Next:          "done",
		}},
		Terminal: map[string]spec.Terminal{"done": {Status: "ok"}},
	}

	stateDir := t.TempDir()
	t.Setenv(journal.EnvStateDir, stateDir)
	e := New(w, t.TempDir())
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	instr, err := e.Submit("run1", "work", 1, []byte(`{"result":"x"}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}: a returned key with no writes: schema must fail closed", instr)
	}
}
