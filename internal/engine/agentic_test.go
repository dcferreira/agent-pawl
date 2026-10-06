package engine

import (
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
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
	// this build's real entry point (pawl run, gated on Validate) can never
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

// TestSubmit_MaxLengthIsFixForward: an over-length string is rejected like a
// type mismatch (a failed attempt carrying last_error), and a valid
// resubmission then succeeds. Covers both the state: cap and the tighter
// writes: cap.
func TestSubmit_MaxLengthIsFixForward(t *testing.T) {
	cases := map[string]string{
		"state cap":  "state:\n  title: {type: string, default: \"\", max_length: 10}\nsteps:\n  - id: work\n    kind: agentic\n    description: d\n    writes: {title: {type: string}}\n",
		"writes cap": "state:\n  title: {type: string, default: \"\"}\nsteps:\n  - id: work\n    kind: agentic\n    description: d\n    writes: {title: {type: string, max_length: 10}}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEngine(t, "workflow: ml\nstart: work\n"+body+"    postcondition: \"true\"\n    attempts: 3\n    next: done\nterminal: {done: {status: ok, message: \"t=${title}\"}}\n")
			if _, err := e.Start("run1", nil); err != nil {
				t.Fatalf("Start: %v", err)
			}
			instr, err := e.Submit("run1", "work", 1, []byte(`{"title":"01234567890"}`))
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			d, ok := instr.(Dispatch)
			if !ok || d.Attempt != 2 {
				t.Fatalf("got %+v, want Dispatch attempt 2 (over-length is a failed attempt)", instr)
			}
			for _, want := range []string{`step "work"`, `key "title"`, "11 characters", "max_length 10"} {
				if !strings.Contains(d.PreviousFailure, want) {
					t.Fatalf("PreviousFailure = %q, want it to contain %q", d.PreviousFailure, want)
				}
			}
			instr, err = e.Submit("run1", "work", 2, []byte(`{"title":"0123456789"}`))
			if err != nil {
				t.Fatalf("Submit 2: %v", err)
			}
			term, ok := instr.(Terminal)
			if !ok || term.Status != "ok" || term.Message != "t=0123456789" {
				t.Fatalf("got %+v, want Terminal{ok, t=0123456789}", instr)
			}
		})
	}
}
