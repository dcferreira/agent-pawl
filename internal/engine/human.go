package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// dispatchHuman enters a human step (a cap check, then STEP_ENTER, mirroring
// dispatchAgentic) and returns the Ask instruction the caller hands to a
// person via AskUserQuestion (design/format-spec.md §B.5, DESIGN.md §3).
// Like agentic, it does not loop: the answer arrives later via SubmitHuman.
func (e *Engine) dispatchHuman(dir string, log *journal.Log, runID string, step *spec.Step, cur journal.Cursor, interrupted bool) (Instruction, error) {
	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	if e.checkCaps(rs, step) {
		instr, next, err := e.routeReserved(dir, log, runID, step, "exhausted", cur.Attempt, false)
		if err != nil {
			return nil, err
		}
		if next != nil {
			return e.runFrom(dir, log, runID, *next)
		}
		return instr, nil
	}

	vals := buildValues(e.Workflow, rs, runID, step.ID, cur.Attempt, rs.Visits[step.ID])
	attempt, key, err := e.beginAttempt(rs, step, cur, vals)
	if err != nil {
		return nil, err
	}
	if _, err := log.Append(journal.Event{
		Kind: journal.KindStepEnter, RunID: runID, Step: step.ID,
		Attempt: attempt, AttemptKey: key,
	}); err != nil {
		return nil, err
	}
	rs, err = replayDir(dir)
	if err != nil {
		return nil, err
	}
	vals = buildValues(e.Workflow, rs, runID, step.ID, attempt, rs.Visits[step.ID])

	options, derr := e.resolveHumanOptions(step, rs)
	if derr != nil {
		if !errors.Is(derr, errOptionsUnavailable) {
			return nil, derr
		}
		// N2's precedent (see dispatchAgentic): an unresolvable options_from:
		// is an authoring bug discovered only at dispatch time, routed via
		// the reserved "failure" outcome rather than thrown — human has no
		// author-facing "failure" outcome of its own (§C), but "failure" is
		// always catch-or-default-routable regardless of kind
		// (resolveTarget/isReservedRoutable), so this reuses the engine's
		// existing escape hatch rather than inventing a new one.
		if jerr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, derr.Error()); jerr != nil {
			return nil, jerr
		}
		return e.routeHuman(dir, log, runID, step, "failure", attempt)
	}

	question, rerr := render.RenderProse(step.Question, vals)
	if rerr != nil {
		question = step.Question
	}

	if _, err := log.Append(journal.Event{
		Kind: journal.KindHumanAsked, RunID: runID, Step: step.ID, Attempt: attempt,
	}); err != nil {
		return nil, err
	}

	_ = interrupted // Ask carries no Interrupted field (point 5) — nothing to set.
	return Ask{
		RunID:    runID,
		Step:     step.ID,
		Attempt:  attempt,
		Question: question,
		Options:  options,
		Multi:    step.Multi,
	}, nil
}

// resolveHumanOptions returns step's display option list, in order: the
// static options: as authored, or, for options_from:, the named state key's
// current value read off rs (combinedRaw — args+state, falling back to
// declared defaults), which design/format-spec.md §B.5 requires be a "state
// key holding a list of strings, resolved at ask time". A missing, empty, or
// wrongly-typed key is errOptionsUnavailable.
func (e *Engine) resolveHumanOptions(step *spec.Step, rs *journal.RunState) ([]string, error) {
	if step.OptionsFrom == "" {
		return step.Options, nil
	}
	raw := combinedRaw(e.Workflow, rs)
	v, ok := raw[step.OptionsFrom]
	if !ok {
		return nil, fmt.Errorf("%w: step %q: options_from: %q is not set", errOptionsUnavailable, step.ID, step.OptionsFrom)
	}
	list, err := toStringList(v)
	if err != nil || len(list) == 0 {
		return nil, fmt.Errorf("%w: step %q: options_from: %q: %v", errOptionsUnavailable, step.ID, step.OptionsFrom, err)
	}
	return list, nil
}

// toStringList coerces v — a state key's raw JSON-decoded value — into a
// list of strings: []string (a Go-side default:), []any of strings (the
// common case: a value that arrived through JSON, e.g. a WRITES event or a
// YAML default: parsed by gopkg.in/yaml.v3 as []any).
func toStringList(v any) ([]string, error) {
	switch x := v.(type) {
	case []string:
		return x, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("list contains a non-string entry (%T)", item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("not a list (got %T)", v)
	}
}

// routeHuman journals the TRANSITION for outcome off a human step and
// continues the run, mirroring routeAgentic exactly.
func (e *Engine) routeHuman(dir string, log *journal.Log, runID string, step *spec.Step, outcome string, attempt int) (Instruction, error) {
	if instr, err := e.preTransitionInvariantBlock(dir, log, runID, step.ID); err != nil {
		return nil, err
	} else if instr != nil {
		return instr, nil
	}
	target, viaCatch, err := resolveTarget(step, outcome)
	if err != nil {
		return nil, err
	}
	if _, err := log.Append(journal.Event{
		Kind: journal.KindTransition, RunID: runID, Step: step.ID, Attempt: attempt,
		Target: target, Outcome: outcome, ViaCatch: viaCatch,
	}); err != nil {
		return nil, err
	}
	instr, next, err := e.afterTransition(dir, log, runID, step.ID, outcome, target)
	if err != nil {
		return nil, err
	}
	if next != nil {
		return e.runFrom(dir, log, runID, *next)
	}
	return instr, nil
}

// humanAnswer is the JSON shape pawl submit accepts for a human step's
// answer (design/format-spec.md §B.5, recorded there as a new paragraph by
// this change): {"selected": ["Option Label"], "other": "free text"}.
// Selected is the option label(s) picked from the static/dynamic list
// (empty/omitted if only free text was given); Other is present when a
// free-text "Other" answer was given, possibly alongside Selected on a
// multi-select that mixes a listed pick with free text.
type humanAnswer struct {
	Selected []string `json:"selected"`
	Other    *string  `json:"other"`
}

// SubmitHuman applies the answer to a live human step's question: it
// refuses any (run, step, attempt) triple other than the one the journal
// says the engine is waiting on; enforces timeout: at submit time (point 3
// — a human step has no background poller, so the deadline is only ever
// checked here); resolves the outcome per design/format-spec.md §B.5 and
// writes the answer into the declared writes: key, if any (except on
// timeout, where nothing is written); and continues the run loop.
func (e *Engine) SubmitHuman(runID, stepID string, attempt int, result json.RawMessage) (Instruction, error) {
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, runID)
	lock, err := journal.AcquireLock(dir, false)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	log, err := journal.OpenLog(dir)
	if err != nil {
		return nil, err
	}
	defer log.Close()

	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	if rs.Terminal() {
		return nil, fmt.Errorf("%w: run %q ended %s", ErrAlreadyTerminal, runID, rs.EndStatus)
	}
	if rs.Cursor.Step != stepID || rs.Cursor.Attempt != attempt {
		return nil, fmt.Errorf("%w: submit for %s/attempt %d, but the run is waiting on %s/attempt %d",
			ErrRefused, stepID, attempt, rs.Cursor.Step, rs.Cursor.Attempt)
	}
	step := e.Workflow.StepByID(stepID)
	if step == nil || step.Kind != "human" {
		return nil, fmt.Errorf("%w: step %q is not a human step awaiting an answer", ErrRefused, stepID)
	}

	timedOut, terr := e.humanDeadlinePassed(dir, stepID, attempt, step.Timeout)
	if terr != nil {
		return nil, terr
	}
	if timedOut {
		// §B.5: "On timeout nothing is written" — unconditional, regardless
		// of what answer was actually submitted.
		return e.routeHuman(dir, log, runID, step, "timeout", attempt)
	}

	options, oerr := e.resolveHumanOptions(step, rs)
	if oerr != nil {
		// Re-resolving options_from: at submit time should be unreachable in
		// practice (dispatchHuman already routed away on failure before ever
		// asking), but resolveHumanOptions is re-run here rather than
		// trusted blind, for the same reason evaluatePostcondition never
		// trusts a cached earlier result.
		return nil, oerr
	}

	outcome, writeVal, verr := resolveHumanAnswer(step, options, result)
	if verr != nil {
		// A bad answer is a submit-time validation error, not a postcondition
		// failure: human has no attempts: machinery to retry into, so this
		// follows advanceDeterministic's I1/I3 hard-failure path (journal a
		// diagnostic, route via the reserved "failure" outcome) rather than
		// the attempt-retry path Submit (agentic) uses for a postcondition
		// failure.
		if jerr := e.journalFailureDiagnostic(log, runID, stepID, attempt, verr.Error()); jerr != nil {
			return nil, jerr
		}
		return e.routeHuman(dir, log, runID, step, "failure", attempt)
	}

	if writeVal != nil && len(step.Writes.Keys) > 0 {
		key := step.Writes.Keys[0]
		coerced := writeVal
		if step.Writes.IsTyped {
			c, cerr := coerceAgenticValue(key, writeVal, step.Writes.Types[key])
			if cerr != nil {
				if jerr := e.journalFailureDiagnostic(log, runID, stepID, attempt, cerr.Error()); jerr != nil {
					return nil, jerr
				}
				return e.routeHuman(dir, log, runID, step, "failure", attempt)
			}
			coerced = c
		}
		if _, err := log.Append(journal.Event{
			Kind: journal.KindWrites, RunID: runID, Step: stepID,
			Attempt: attempt, Writes: map[string]any{key: coerced},
		}); err != nil {
			return nil, err
		}
	}

	return e.routeHuman(dir, log, runID, step, outcome, attempt)
}

// humanDeadlinePassed reports whether stepID's most recent HUMAN_ASKED event
// for attempt is older than timeoutStr's parsed duration, as of now (point
// 3): the mechanism is deliberately re-derived from the journal's own event
// Time on every submit, rather than cached anywhere, since SubmitHuman runs
// in a brand-new process with no memory but the journal.
func (e *Engine) humanDeadlinePassed(dir, stepID string, attempt int, timeoutStr string) (bool, error) {
	d, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return false, fmt.Errorf("engine: step %q: timeout: %q: %w", stepID, timeoutStr, err)
	}
	events, err := journal.ReadEvents(dir)
	if err != nil {
		return false, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind == journal.KindHumanAsked && ev.Step == stepID && ev.Attempt == attempt {
			return time.Now().After(ev.Time.Add(d)), nil
		}
	}
	return false, fmt.Errorf("engine: internal error: no HUMAN_ASKED event recorded for %s/attempt %d", stepID, attempt)
}

// resolveHumanAnswer implements design/format-spec.md §B.5's resolution
// table (elaborated in this change's point 2):
//
//   - options_from: set, or multi: true — outcome is always the reserved
//     "chosen"; the written value is a JSON array combining selected plus
//     other (multi: true), or a single value: other if set, else the one
//     selected entry (multi: false with options_from:).
//   - static options:, multi: false, no options_from: — exactly one of (a)
//     selected names exactly one declared option (outcome: that option
//     label), or (b) other is set and selected is empty (outcome: "chosen").
//     Anything else is a validation error.
//
// options is the resolved display list (resolveHumanOptions's result),
// already scoped to the right source (static or options_from:) — a bad
// selected entry is validated against it only in the static-single-select
// branch, per point 2: the dynamic/multi branch accepts whatever selected
// contains without second-guessing it (design/format-spec.md: "branch on
// the written value from a following deterministic router step").
func resolveHumanAnswer(step *spec.Step, options []string, raw json.RawMessage) (outcome string, writeVal any, err error) {
	var ans humanAnswer
	if len(raw) == 0 {
		return "", nil, fmt.Errorf("step %q: submitted answer is empty", step.ID)
	}
	if uerr := json.Unmarshal(raw, &ans); uerr != nil {
		return "", nil, fmt.Errorf("step %q: submitted answer is not a JSON object: %v", step.ID, uerr)
	}

	dynamic := step.OptionsFrom != "" || step.Multi
	if dynamic {
		if step.Multi {
			list := append([]string{}, ans.Selected...)
			if ans.Other != nil {
				list = append(list, *ans.Other)
			}
			if len(list) == 0 {
				return "", nil, fmt.Errorf("step %q: submitted answer has neither selected nor other", step.ID)
			}
			return "chosen", toAnySlice(list), nil
		}
		// options_from:, multi: false.
		switch {
		case ans.Other != nil:
			return "chosen", *ans.Other, nil
		case len(ans.Selected) == 1:
			return "chosen", ans.Selected[0], nil
		default:
			return "", nil, fmt.Errorf("step %q: options_from: expects exactly one of selected (one entry) or other", step.ID)
		}
	}

	// Static options:, multi: false.
	hasOther := ans.Other != nil
	switch {
	case len(ans.Selected) == 1 && !hasOther:
		picked := ans.Selected[0]
		if !containsString(options, picked) {
			return "", nil, fmt.Errorf("step %q: selected %q is not a declared option", step.ID, picked)
		}
		return picked, picked, nil
	case len(ans.Selected) == 0 && hasOther:
		return "chosen", *ans.Other, nil
	default:
		return "", nil, fmt.Errorf("step %q: expects exactly one of selected (one entry, a declared option) or other", step.ID)
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func toAnySlice(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}
