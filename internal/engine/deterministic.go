package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dcferreira/agent-pawl/internal/emit"
	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// deterministicAttempt is the result of executing and evaluating one
// attempt of a deterministic step's body (run: through postcondition:),
// stopping short of any routing decision.
type deterministicAttempt struct {
	// hardFailed is true once this attempt's own failure diagnostic has
	// already been journaled (I1/I3, or a timeout): the caller must treat
	// the step as failed with outcome "failure" and must not evaluate (or
	// act on) a postcondition.
	hardFailed bool
	// outcome is the exec result's own success/failure outcome (emit),
	// valid only when hardFailed is false.
	outcome string
	// cr is the postcondition result, valid only when hardFailed is false.
	cr checkResult
}

// runDeterministicAttempt executes step's run: once at the given attempt
// (STEP_ENTER must already be journaled by the caller), journals its WRITES
// and any hard-failure diagnostic, and evaluates its postcondition —
// everything advanceDeterministic's loop body does up to (but not
// including) the routing decision, factored out so a kind: parallel
// branch (execBranchDeterministic, parallel.go) can reuse the exact same
// exec/journal/evaluate machinery for its own single, no-retry pass — a
// branch owns no routing or retry budget of its own (task 1's validation
// forbids next:/outcomes:/attempts: on one).
//
// It returns the possibly-updated RunState (refreshed after a WRITES
// journal, exactly as advanceDeterministic's loop already did inline)
// alongside the attempt result, since the caller needs post-write state for
// whatever it does next.
func (e *Engine) runDeterministicAttempt(dir string, log *journal.Log, runID string, step *spec.Step, attempt int, rs *journal.RunState) (deterministicAttempt, *journal.RunState, error) {
	vals := buildValues(e.Workflow, rs, runID, step.ID, attempt, rs.Visits[step.ID])

	result, timedOut, stdout, stderr, execErr := e.execDeterministic(step, vals)
	e.writeStepOutput(dir, step.ID, attempt, stdout, stderr)

	if execErr != nil {
		if errors.Is(execErr, emit.ErrParse) {
			// I3: unintelligible stdout is an authoring bug, not a Go error
			// to throw out of the run loop — see the caller for why this is
			// routed, not thrown.
			if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, execErr.Error()); derr != nil {
				return deterministicAttempt{}, rs, derr
			}
			return deterministicAttempt{hardFailed: true}, rs, nil
		}
		return deterministicAttempt{}, rs, execErr
	}
	if timedOut {
		text := fmt.Sprintf("step %q exceeded the wall-clock ceiling of %s", step.ID, e.Timeout)
		if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, text); derr != nil {
			return deterministicAttempt{}, rs, derr
		}
		return deterministicAttempt{hardFailed: true}, rs, nil
	}
	if result.Writes != nil {
		if _, err := log.Append(journal.Event{
			Kind: journal.KindWrites, RunID: runID, Step: step.ID,
			Attempt: attempt, Writes: result.Writes,
		}); err != nil {
			return deterministicAttempt{}, rs, err
		}
		var err error
		rs, err = replayDir(dir)
		if err != nil {
			return deterministicAttempt{}, rs, err
		}
		vals = buildValues(e.Workflow, rs, runID, step.ID, attempt, rs.Visits[step.ID])
	}
	if result.Outcome == "failure" {
		// I1: a non-zero exit never runs a postcondition, so nothing would
		// otherwise set last_error for it (DESIGN.md §3 requires it set
		// here).
		text := strings.TrimSpace(stderr)
		if text == "" {
			text = strings.TrimSpace(stdout)
		}
		if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, text); derr != nil {
			return deterministicAttempt{}, rs, derr
		}
		return deterministicAttempt{hardFailed: true}, rs, nil
	}

	cr, err := e.evaluatePostcondition(step, combinedRaw(e.Workflow, rs), vals)
	if err != nil {
		return deterministicAttempt{}, rs, err
	}
	return deterministicAttempt{outcome: result.Outcome, cr: cr}, rs, nil
}

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
	// hardRetry carries a resumed hard-retry-in-progress count (Event.
	// HardRetry, journal.go) for the very first pass through the loop below
	// only: a crash mid hard-retry backoff leaves cur.HardRetry set to the
	// last try number that was journaled, and resuming must re-run that same
	// try, not advance past it (mirrors how a crash never advances the
	// attempts:-level attempt number — TestAttempts_NotAdvancedByCrash).
	// Every subsequent postcondition-level attempt in this same call (this
	// loop looping back after a postcondition failure) starts its own
	// retry: budget fresh, since retry: retries one attempt's body.
	hardRetry := cur.HardRetry

	for {
		if _, err := log.Append(journal.Event{
			Kind: journal.KindStepEnter, RunID: runID, Step: step.ID,
			Attempt: attempt, AttemptKey: key, Retry: retry, HardRetry: hardRetry,
		}); err != nil {
			return nil, nil, err
		}
		rs, err = replayDir(dir)
		if err != nil {
			return nil, nil, err
		}

		at, _, err := e.runDeterministicAttempt(dir, log, runID, step, attempt, rs)
		if err != nil {
			return nil, nil, err
		}

		// retry: (design/format-spec.md §B.16) retries a hard-failed body
		// before any outcome is resolved — never a postcondition failure,
		// which stays attempts:'s domain (at.hardFailed is only ever set for
		// the hard-failure cases: non-zero exit, wall-clock timeout,
		// unintelligible stdout — see deterministicAttempt.hardFailed).
		for at.hardFailed && step.Retry != nil && hardRetry+1 < step.Retry.MaxAttempts {
			tryNumber := hardRetry + 1
			backoff, berr := time.ParseDuration(step.Retry.Backoff)
			if berr != nil {
				// Validate rejects an unparseable backoff:; this is only
				// reachable from a *Workflow built by hand (a test literal)
				// that skipped Validate.
				return nil, nil, fmt.Errorf("engine: step %q: retry.backoff: %q is not a duration (validator should have rejected this): %w", step.ID, step.Retry.Backoff, berr)
			}
			e.sleep(backoff * time.Duration(tryNumber))
			hardRetry = tryNumber
			if _, err := log.Append(journal.Event{
				Kind: journal.KindStepEnter, RunID: runID, Step: step.ID,
				Attempt: attempt, AttemptKey: key, Retry: retry, HardRetry: hardRetry,
			}); err != nil {
				return nil, nil, err
			}
			rs, err = replayDir(dir)
			if err != nil {
				return nil, nil, err
			}
			at, _, err = e.runDeterministicAttempt(dir, log, runID, step, attempt, rs)
			if err != nil {
				return nil, nil, err
			}
		}
		// This attempt's own hard-retry budget (if any) is spent; the next
		// postcondition-level attempt (if the loop continues) starts fresh.
		hardRetry = 0

		if at.hardFailed {
			return e.routeReserved(dir, log, runID, step, "failure", attempt)
		}
		cr := at.cr

		if cr.OK {
			if step.Postcondition != nil {
				if _, err := log.Append(journal.Event{
					Kind: journal.KindPostcondition, RunID: runID, Step: step.ID,
					Attempt: attempt, OK: true, Soft: cr.Soft, AttemptKey: key,
				}); err != nil {
					return nil, nil, err
				}
			}
			target, viaCatch, err := resolveTarget(step, at.outcome)
			if err != nil {
				return nil, nil, err
			}
			if _, err := log.Append(journal.Event{
				Kind: journal.KindTransition, RunID: runID, Step: step.ID, Attempt: attempt,
				Target: target, Outcome: at.outcome, ViaCatch: viaCatch,
			}); err != nil {
				return nil, nil, err
			}
			return e.afterTransition(dir, log, runID, step.ID, at.outcome, target)
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
