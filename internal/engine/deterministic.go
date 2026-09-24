package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dcferreira/agent-pawl/internal/emit"
	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// deterministicAttempt is the result of executing and evaluating one
// attempt of a deterministic step's body (run: through postcondition:),
// stopping short of any routing decision.
type deterministicAttempt struct {
	// hardFailed is true once this attempt's own failure diagnostic has
	// already been journaled (I1/I3, a timeout, or an exit-0 author-printed
	// reserved "failure" token): the caller must treat the step as failed
	// with outcome "failure" and must not evaluate (or act on) a
	// postcondition — exactly the pre-retry: behavior for all of these
	// cases.
	hardFailed bool
	// retryable is true only when hardFailed is also true for one of
	// §B.16's three actual hard-failure cases — a non-zero exit, the
	// wall-clock timeout, or unintelligible stdout (ErrParse) — and false
	// for an exit-0 author-printed reserved "failure" token, which is a
	// clean routed tick that happens to route to failure (mirrors wait's
	// `it.Token == "failure" && !hardFailed` in poll.go,
	// TestRetry_WaitExitZeroFailureTokenIsNotHardFailure). Only retryable
	// attempts may be retried by retry: (see advanceDeterministic's
	// hard-retry loop, which checks this instead of hardFailed).
	retryable bool
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
//
// lastErrorOverride, when non-nil, replaces the ${last_error} value that
// would otherwise come from rs.LastError: advanceDeterministic's hard-retry
// loop (§B.16) passes the attempt's own frozen AttemptLastError here so a
// retried try never sees the diagnostic journaled by the previous hard-
// failed try of the *same* attempt. A caller with no such concern (the
// plain first-try path, or a kind: parallel branch, which owns no retry:
// budget of its own) passes nil and gets the ordinary rs.LastError.
func (e *Engine) runDeterministicAttempt(dir string, log *journal.Log, runID string, step *spec.Step, attempt int, rs *journal.RunState, lastErrorOverride *string) (deterministicAttempt, *journal.RunState, error) {
	vals := buildValues(e.Workflow, rs, runID, step.ID, attempt, rs.Visits[step.ID])
	if lastErrorOverride != nil {
		vals["last_error"] = render.StringValue(*lastErrorOverride)
	}

	result, timedOut, stdout, stderr, exitCode, execErr := e.execDeterministic(step, vals)
	e.writeStepOutput(dir, step.ID, attempt, stdout, stderr)

	if execErr != nil {
		if errors.Is(execErr, emit.ErrParse) {
			// I3: unintelligible stdout is an authoring bug, not a Go error
			// to throw out of the run loop — see the caller for why this is
			// routed, not thrown.
			if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, execErr.Error()); derr != nil {
				return deterministicAttempt{}, rs, derr
			}
			return deterministicAttempt{hardFailed: true, retryable: true}, rs, nil
		}
		return deterministicAttempt{}, rs, execErr
	}
	if timedOut {
		text := fmt.Sprintf("step %q exceeded the wall-clock ceiling of %s", step.ID, e.Timeout)
		if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, text); derr != nil {
			return deterministicAttempt{}, rs, derr
		}
		return deterministicAttempt{hardFailed: true, retryable: true}, rs, nil
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
		if lastErrorOverride != nil {
			vals["last_error"] = render.StringValue(*lastErrorOverride)
		}
	}
	if result.Outcome == "failure" {
		// I1: a non-zero exit never runs a postcondition, so nothing would
		// otherwise set last_error for it (DESIGN.md §3 requires it set
		// here). An exit-0 author-printed reserved "failure" token takes
		// the exact same hardFailed routing (straight to routeReserved,
		// no postcondition, no attempts: retry) as it did before retry:
		// existed — only retryable distinguishes the two, so retry:
		// retries the former but never the latter.
		text := strings.TrimSpace(stderr)
		if text == "" {
			text = strings.TrimSpace(stdout)
		}
		if derr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, text); derr != nil {
			return deterministicAttempt{}, rs, derr
		}
		return deterministicAttempt{hardFailed: true, retryable: exitCode != 0}, rs, nil
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
		// attemptLastError freezes ${last_error} as this attempt's first try
		// sees it (rs.AttemptLastError, journal/replay.go) — captured once
		// here and reused, unchanged, for every hard-retry of this same
		// attempt below, so a retried try never sees the diagnostic the
		// previous try's own hard failure just journaled (§B.16). It also
		// covers a crash-resume mid hard-retry: rs.AttemptLastError was
		// reconstructed by the full Replay from the attempt's own first
		// STEP_ENTER (HardRetry: 0), not from whatever the log's tail
		// happens to be now.
		attemptLastError := rs.AttemptLastError

		at, _, err := e.runDeterministicAttempt(dir, log, runID, step, attempt, rs, &attemptLastError)
		if err != nil {
			return nil, nil, err
		}

		// retry: (design/format-spec.md §B.16) retries a hard-failed body
		// before any outcome is resolved — never a postcondition failure,
		// which stays attempts:'s domain, and never an exit-0 author-
		// printed reserved "failure" token, which is hardFailed (it takes
		// the same no-postcondition, no-attempts:-retry routing) but not
		// retryable — see deterministicAttempt.retryable.
		for at.retryable && step.Retry != nil && hardRetry+1 < step.Retry.MaxAttempts {
			tryNumber := hardRetry + 1
			backoff, berr := time.ParseDuration(step.Retry.Backoff)
			if berr != nil {
				// Validate rejects an unparseable backoff:; this is only
				// reachable from a *Workflow built by hand (a test literal)
				// that skipped Validate.
				return nil, nil, fmt.Errorf("engine: step %q: retry.backoff: %q is not a duration (validator should have rejected this): %w", step.ID, step.Retry.Backoff, berr)
			}
			sleepFor := backoff * time.Duration(tryNumber)
			e.logRetry("pawl: step %q hard-failed (try %d of %d); retrying in %s", step.ID, tryNumber, step.Retry.MaxAttempts, sleepFor)
			e.sleep(sleepFor)
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
			at, _, err = e.runDeterministicAttempt(dir, log, runID, step, attempt, rs, &attemptLastError)
			if err != nil {
				return nil, nil, err
			}
		}
		// This attempt's own hard-retry budget (if any) is spent; the next
		// postcondition-level attempt (if the loop continues) starts fresh.
		// hardRetried records, before the reset, whether at least one
		// hard-failure diagnostic was journaled for *this* attempt/key
		// (each hard-failed try journals one via journalFailureDiagnostic
		// inside runDeterministicAttempt) before it went on to succeed.
		hardRetried := hardRetry > 0
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
			} else if hardRetried {
				// A step with no postcondition: never otherwise journals a
				// KindPostcondition{OK:true}, so without this, a try that
				// hard-failed (journaling OK:false, per journalFailureDiagnostic)
				// and then recovered on retry: would leave that earlier
				// try's diagnostic stuck in ${last_error} (Replay clears it
				// only on OK:true) and would leave Replay's per-step
				// postcondition-ok flag false, which would wrongly block the
				// §B.4 attempt-key-budget clearing a clean non-catch
				// TRANSITION is supposed to do. Journal one here, exactly as
				// a declared postcondition's own pass would, now that this
				// attempt has actually succeeded.
				if _, err := log.Append(journal.Event{
					Kind: journal.KindPostcondition, RunID: runID, Step: step.ID,
					Attempt: attempt, OK: true, AttemptKey: key,
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
