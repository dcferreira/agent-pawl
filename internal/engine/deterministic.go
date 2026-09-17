package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dcferreira/agentic-workflow-fsm/internal/emit"
	"github.com/dcferreira/agentic-workflow-fsm/internal/journal"
	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// advanceDeterministic runs one "visit" of a deterministic step to
// completion: the cap check, the full attempt-retry loop (re-exec on
// postcondition failure, fix-forward, up to attempts:), and the eventual
// transition. It returns either a non-nil cursor (the run continues at
// another step — runFrom loops) or a non-nil Instruction (a terminal was
// reached).
func (e *Engine) advanceDeterministic(dir string, log *journal.Log, runID string, cur journal.Cursor) (Instruction, *journal.Cursor, error) {
	step := e.Workflow.StepByID(cur.Step)

	rs, err := replayDir(dir)
	if err != nil {
		return nil, nil, err
	}
	if e.checkCaps(rs, step) {
		return e.routeReserved(dir, log, runID, step, "exhausted", cur.Attempt)
	}

	vals := buildValues(e.Workflow, rs, runID, step.ID, cur.Attempt, rs.Visits[step.ID])
	attempt, key, err := e.beginAttempt(rs, step, cur, vals)
	if err != nil {
		return nil, nil, err
	}
	retry := false

	for {
		if _, err := log.Append(journal.Event{
			Kind: journal.KindStepEnter, RunID: runID, Step: step.ID,
			Attempt: attempt, AttemptKey: key, Retry: retry,
		}); err != nil {
			return nil, nil, err
		}
		rs, err = replayDir(dir)
		if err != nil {
			return nil, nil, err
		}
		vals = buildValues(e.Workflow, rs, runID, step.ID, attempt, rs.Visits[step.ID])

		result, timedOut, stdout, stderr, execErr := e.execDeterministic(step, vals)
		e.writeStepOutput(dir, step.ID, attempt, stdout, stderr)

		if execErr != nil {
			if errors.Is(execErr, emit.ErrParse) {
				// I3: unintelligible stdout is an authoring bug, not a Go
				// error to throw out of the run loop — throwing it left a
				// dangling STEP_ENTER with no TRANSITION/RUN_END, wedging
				// the run so Resume just re-entered and died identically.
				// Route it exactly like any other hard failure instead.
				if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, execErr.Error()); derr != nil {
					return nil, nil, derr
				}
				return e.routeReserved(dir, log, runID, step, "failure", attempt)
			}
			return nil, nil, execErr
		}
		if timedOut {
			text := fmt.Sprintf("step %q exceeded the wall-clock ceiling of %s", step.ID, e.Timeout)
			if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, text); derr != nil {
				return nil, nil, derr
			}
			return e.routeReserved(dir, log, runID, step, "failure", attempt)
		}
		if result.Writes != nil {
			if _, err := log.Append(journal.Event{
				Kind: journal.KindWrites, RunID: runID, Step: step.ID,
				Attempt: attempt, Writes: result.Writes,
			}); err != nil {
				return nil, nil, err
			}
			rs, err = replayDir(dir)
			if err != nil {
				return nil, nil, err
			}
			vals = buildValues(e.Workflow, rs, runID, step.ID, attempt, rs.Visits[step.ID])
		}
		if result.Outcome == "failure" {
			// I1: a non-zero exit never runs a postcondition, so nothing
			// would otherwise set last_error for it (DESIGN.md §3 requires
			// it set here).
			text := strings.TrimSpace(stderr)
			if text == "" {
				text = strings.TrimSpace(stdout)
			}
			if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, text); derr != nil {
				return nil, nil, derr
			}
			return e.routeReserved(dir, log, runID, step, "failure", attempt)
		}

		cr, err := e.evaluatePostcondition(step, combinedRaw(e.Workflow, rs), vals)
		if err != nil {
			return nil, nil, err
		}

		if cr.OK {
			if step.Postcondition != nil {
				if _, err := log.Append(journal.Event{
					Kind: journal.KindPostcondition, RunID: runID, Step: step.ID,
					Attempt: attempt, OK: true, Soft: cr.Soft, AttemptKey: key,
				}); err != nil {
					return nil, nil, err
				}
			}
			target, viaCatch, err := resolveTarget(step, result.Outcome)
			if err != nil {
				return nil, nil, err
			}
			if _, err := log.Append(journal.Event{
				Kind: journal.KindTransition, RunID: runID, Step: step.ID, Attempt: attempt,
				Target: target, Outcome: result.Outcome, ViaCatch: viaCatch,
			}); err != nil {
				return nil, nil, err
			}
			return e.afterTransition(dir, log, runID, step.ID, result.Outcome, target)
		}

		nextKey := key
		if step.AttemptKey == "" {
			nextKey = hashFailureText(cr.Text)
		}
		if _, err := log.Append(journal.Event{
			Kind: journal.KindPostcondition, RunID: runID, Step: step.ID, Attempt: attempt,
			OK: false, Text: cr.Text, Soft: cr.Soft, AttemptKey: nextKey,
		}); err != nil {
			return nil, nil, err
		}
		rs, err = replayDir(dir)
		if err != nil {
			return nil, nil, err
		}

		// C1: re-check the visit/step caps before looping back into another
		// attempt, not only once at the top of this call. Given Retry-aware
		// Visits accounting (an attempt-retry never advances Visits), this
		// is a defensive no-op in the common case — but it is what the
		// reviewer asked for explicitly, and it means a future change to
		// how Visits is accounted can never silently reopen the "un-enforced
		// within a single visit" half of the finding.
		if e.checkCaps(rs, step) {
			return e.routeReserved(dir, log, runID, step, "exhausted", attempt)
		}

		nextAttempt := nextTryNumber(attempt, rs.Attempts[journal.AttemptRef{Step: step.ID, Key: nextKey}])
		if nextAttempt > step.Attempts {
			return e.routeReserved(dir, log, runID, step, "failure", attempt)
		}
		attempt, key = nextAttempt, nextKey
		retry = true
	}
}

// routeReserved resolves and journals the transition for a reserved outcome
// (failure or exhausted) and continues via afterTransition.
func (e *Engine) routeReserved(dir string, log *journal.Log, runID string, step *spec.Step, outcome string, attempt int) (Instruction, *journal.Cursor, error) {
	target, viaCatch, err := resolveTarget(step, outcome)
	if err != nil {
		return nil, nil, err
	}
	if _, err := log.Append(journal.Event{
		Kind: journal.KindTransition, RunID: runID, Step: step.ID, Attempt: attempt,
		Target: target, Outcome: outcome, ViaCatch: viaCatch,
	}); err != nil {
		return nil, nil, err
	}
	return e.afterTransition(dir, log, runID, step.ID, outcome, target)
}
