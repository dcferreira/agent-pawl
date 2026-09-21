package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// TestDispatchHuman_StaticSingleSelectHappyPath: a plain static-option pick
// with no chosen: route (the choose_reviewer / human-approval example
// shape) routes on the picked option's own label and, since writes: is
// declared, writes that label into it (point 1: writes: is populated on
// every path a declared key is present, not only via chosen:).
func TestDispatchHuman_StaticSingleSelectHappyPath(t *testing.T) {
	const yaml = `
workflow: human-static
start: ask
state:
  reviewer: {type: string, default: ""}
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options: [alice, bob]
    timeout: 24h
    writes: [reviewer]
    outcomes:
      alice: done
      bob: done
      timeout: blocked
terminal:
  done: {status: ok, message: "reviewer=${reviewer}"}
  blocked: {status: blocked}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a, ok := instr.(Ask)
	if !ok {
		t.Fatalf("got %T, want Ask", instr)
	}
	if a.Question != "Who reviews?" || a.Multi {
		t.Errorf("Ask = %+v", a)
	}
	if len(a.Options) != 2 || a.Options[0] != "alice" || a.Options[1] != "bob" {
		t.Errorf("Ask.Options = %v, want [alice bob]", a.Options)
	}

	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["alice"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}", instr)
	}
	if term.Message != "reviewer=alice" {
		t.Errorf("terminal message = %q, want it to show the written reviewer", term.Message)
	}
}

// TestDispatchHuman_StaticOtherFreeText: an "Other" free-text answer on a
// static-options step routes via the reserved "chosen" outcome and writes
// the free text.
func TestDispatchHuman_StaticOtherFreeText(t *testing.T) {
	const yaml = `
workflow: human-other
start: ask
state:
  reviewer: {type: string, default: ""}
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options: [alice, bob]
    timeout: 24h
    writes: [reviewer]
    outcomes:
      alice: done
      bob: done
      chosen: done
      timeout: blocked
terminal:
  done: {status: ok, message: "reviewer=${reviewer}"}
  blocked: {status: blocked}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a := instr.(Ask)

	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"other":"carol"}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" || term.Message != "reviewer=carol" {
		t.Fatalf("got %+v, want Terminal{ok, reviewer=carol}", instr)
	}
}

// TestDispatchHuman_OptionsFromMultiHappyPath: options_from: + multi: true
// always routes "chosen" and writes a JSON list combining selected and
// other.
func TestDispatchHuman_OptionsFromMultiHappyPath(t *testing.T) {
	const yaml = `
workflow: human-multi
start: ask
state:
  candidates: {type: json, default: ["alice", "bob", "carol"]}
  picked: {type: json, default: []}
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options_from: candidates
    multi: true
    timeout: 24h
    writes: {picked: {type: json}}
    outcomes:
      chosen: done
      timeout: blocked
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a, ok := instr.(Ask)
	if !ok {
		t.Fatalf("got %T, want Ask", instr)
	}
	if len(a.Options) != 3 {
		t.Fatalf("Ask.Options = %v, want the 3 resolved candidates", a.Options)
	}

	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["alice","bob"],"other":"dave"}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{ok}", instr)
	}

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wrote map[string]any
	for _, ev := range events {
		if ev.Kind == journal.KindWrites && ev.Step == "ask" {
			wrote = ev.Writes
		}
	}
	list, ok := wrote["picked"].([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("picked = %#v, want a 3-element JSON list [alice bob dave]", wrote["picked"])
	}
}

// TestSubmitHuman_BadSelectedIsSubmitTimeError: a selected value that
// doesn't match any declared static option is a submit-time validation
// error (point 2), routed like advanceDeterministic's hard-failure path
// (journalled, routed via "failure") since human has no attempts: to retry
// into.
func TestSubmitHuman_BadSelectedIsSubmitTimeError(t *testing.T) {
	const yaml = `
workflow: human-bad-selected
start: ask
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options: [alice, bob]
    timeout: 24h
    outcomes:
      alice: done
      bob: done
      timeout: blocked
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a := instr.(Ask)

	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["nobody"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked}: an undeclared option must be a hard error, not silently accepted", instr)
	}
}

// TestSubmitHuman_TimeoutExpiry: once the HUMAN_ASKED deadline has passed,
// SubmitHuman routes "timeout" unconditionally, regardless of the submitted
// answer, and nothing is written (§B.5: "On timeout nothing is written").
// The journal's own HUMAN_ASKED event Time is rewritten into the past
// directly, rather than sleeping past a real timeout: (idiom matched to
// this package's other timeout-ish tests, which manipulate journal state
// rather than the wall clock).
func TestSubmitHuman_TimeoutExpiry(t *testing.T) {
	const yaml = `
workflow: human-timeout
start: ask
state:
  reviewer: {type: string, default: ""}
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options: [alice, bob]
    timeout: 1s
    writes: [reviewer]
    outcomes:
      alice: done
      bob: done
      timeout: blocked
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a := instr.(Ask)

	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	rewriteEventTimeInPast(t, dir, journal.KindHumanAsked, "ask", 2*time.Second)

	instr, err = e.SubmitHuman("run1", "ask", a.Attempt, []byte(`{"selected":["alice"]}`))
	if err != nil {
		t.Fatalf("SubmitHuman: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want Terminal{blocked} (timeout: blocked)", instr)
	}

	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == journal.KindWrites && ev.Step == "ask" {
			t.Fatalf("a WRITES event was journalled for a timed-out human step: %+v", ev)
		}
		if ev.Kind == journal.KindTransition && ev.Step == "ask" && ev.Outcome != "timeout" {
			t.Fatalf("transition outcome = %q, want %q", ev.Outcome, "timeout")
		}
	}
}

// TestResume_HumanStepReasksSameQuestion: crash-resuming a still-live human
// step regenerates the same Ask rather than erroring (the Resume path added
// alongside dispatchAgentic's).
func TestResume_HumanStepReasksSameQuestion(t *testing.T) {
	const yaml = `
workflow: human-resume
start: ask
steps:
  - id: ask
    kind: human
    question: "Who reviews?"
    options: [alice, bob]
    timeout: 24h
    outcomes:
      alice: done
      bob: done
      timeout: blocked
terminal:
  done: {status: ok}
  blocked: {status: blocked}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := instr.(Ask); !ok {
		t.Fatalf("got %T, want Ask", instr)
	}

	instr, err = e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	a, ok := instr.(Ask)
	if !ok {
		t.Fatalf("got %T, want Ask again after resume", instr)
	}
	if a.Question != "Who reviews?" || len(a.Options) != 2 {
		t.Errorf("resumed Ask = %+v, want the same question/options", a)
	}
}

// rewriteEventTimeInPast finds the most recent event of kind for step in
// dir's events.jsonl and rewrites its Time field to shift bySince into the
// past — a direct-journal-edit idiom for driving a timeout deterministically
// without a real sleep.
func rewriteEventTimeInPast(t *testing.T, dir string, kind journal.Kind, step string, shift time.Duration) {
	t.Helper()
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == kind && events[i].Step == step {
			events[i].Time = events[i].Time.Add(-shift)
			break
		}
	}
	var sb strings.Builder
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
