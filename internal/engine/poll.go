package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dcferreira/agent-pawl/internal/emit"
	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// ErrPollNotCurrent marks the one refusal DESIGN.md §3 asks Poll to make
// quietly: "it exits early, doing nothing, if the run directory has gone or
// the run's current step is no longer this step". That is a *race the design
// expects* — another process submitted, the run was abandoned, the model
// re-ran an old WAIT line — not a failure, so the caller reports it and exits
// 0 rather than treating it as an error (see cli.cmdPoll). It is a distinct
// sentinel precisely so the caller can tell it apart from a genuine refusal
// (a poll against a step that is not a wait step at all, which is a real
// mistake and stays a hard error).
var ErrPollNotCurrent = errors.New("engine: the run is no longer waiting at this step")

// PollIteration is one turn of the poll loop, handed to Poll's observer so
// the caller can show that the loop is alive (a wait may be hours long, and
// total silence under Monitor is indistinguishable from a hang). It is
// reporting only: Poll's behaviour does not depend on what the observer does.
type PollIteration struct {
	// N is the 1-based iteration number within this pawl poll invocation.
	N int
	// Elapsed is how long the run has been parked at this step, measured
	// from the STEP_ENTER that parked it — not from this process's start.
	Elapsed time.Duration
	// Line is the last non-empty stdout line this iteration read (§B.1),
	// already C0-escaped; "" when the command printed nothing.
	Line string
	// ExitCode is the poll: command's exit status (-1 if it could not be run
	// or hit the wall-clock ceiling).
	ExitCode int
	// Token is the outcome token read off Line (or the reserved outcome a
	// non-zero exit / a tokenless step implies).
	Token string
	// Routed is true when Token is one this step routes: the iteration that
	// ends the loop.
	Routed bool
	// TimedOut is true on the final iteration when timeout: expired instead:
	// the loop ends on the reserved timeout outcome.
	TimedOut bool
	// Next is how long the loop will sleep before the next iteration; 0 when
	// this iteration ended the loop.
	Next time.Duration
}

// Poll implements `pawl poll --run <id> --step <name>` (DESIGN.md §3,
// design/format-spec.md §13): it re-runs the wait step's poll: every every:,
// reads each iteration's last non-empty stdout line per §B.1, and the first
// iteration carrying a *routed* token ends the loop — at which point Poll
// does internally exactly what Submit does (apply the payload, evaluate the
// postcondition, resolve the outcome, take the transition, run whatever
// deterministic steps follow) and returns the resulting instruction for the
// caller to print. On timeout: expiry it does the same with outcome
// "timeout".
//
// Three deliberate rulings live here:
//
//  1. **An unrouted token is not an error.** A line that is well-formed under
//     §B.1 but names no declared outcome (the wait-for-build example's
//     PENDING) simply does not end the loop: DESIGN.md §3 and §13 both say
//     "the first iteration whose ... line carries a *routed* token ends the
//     loop", which only means anything if unrouted iterations are expected.
//     This is where wait and deterministic legitimately diverge — see
//     emit.Routed's doc comment for why the identical line is ErrParse there.
//
//  2. **The timeout clock starts when the run parked**, at the STEP_ENTER
//     event's own journalled timestamp, not when this process started. A
//     poller killed and restarted (Monitor died, the session crashed, the
//     model re-read an old WAIT line) must not silently restart an hours-long
//     deadline every time; the journal is the run's only memory across
//     processes (DESIGN.md §4), so it is the only honest origin for it.
//
//  3. **No lock is held between iterations.** A wait is hours long, and
//     taking the run lock for its duration would turn every other command
//     against that run — pawl status, pawl abandon, and the pawl submit
//     refusal this design depends on being *legible* — into a lock error.
//     The lock is taken only for the submit at the end, and the cursor is
//     re-checked under it, so the window cannot produce a double transition.
func (e *Engine) Poll(runID, stepID string, observe func(PollIteration)) (Instruction, error) {
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, runID)

	step, parkedAt, err := e.pollPrecheck(dir, runID, stepID)
	if err != nil {
		return nil, err
	}

	every, err := time.ParseDuration(step.Every)
	if err != nil {
		return nil, fmt.Errorf("engine: step %q: every: %q is not a duration (validator should have rejected this): %w", step.ID, step.Every, err)
	}
	timeout, err := time.ParseDuration(step.Timeout)
	if err != nil {
		return nil, fmt.Errorf("engine: step %q: timeout: %q is not a duration (validator should have rejected this): %w", step.ID, step.Timeout, err)
	}
	deadline := parkedAt.Add(timeout)

	for n := 1; ; n++ {
		// Re-check every iteration, not only at entry: the run may have been
		// abandoned, or advanced by another process, while this loop slept.
		if _, _, err := e.pollPrecheck(dir, runID, stepID); err != nil {
			return nil, err
		}

		if !e.now().Before(deadline) {
			if observe != nil {
				observe(PollIteration{N: n, Elapsed: e.now().Sub(parkedAt), Token: "timeout", Routed: true, TimedOut: true})
			}
			return e.completeWait(dir, runID, stepID, "timeout", nil, "")
		}

		it := PollIteration{N: n, Elapsed: e.now().Sub(parkedAt)}
		stdout, stderr, exitCode, execErr := e.execPoll(step, dir, runID, n)
		if execErr != nil && !errors.Is(execErr, errTimeout) {
			return nil, execErr
		}
		it.ExitCode = exitCode
		it.Line = emit.EscapeC0(emit.LastNonEmptyLine(stdout))
		it.Token, it.Routed = emit.Routed(stdout, exitCode, step)
		if errors.Is(execErr, errTimeout) {
			// The poll: command itself blew the engine-wide wall-clock
			// ceiling: a hard failure exactly as it is for a deterministic
			// run: (design/format-spec.md §3), not a "not yet".
			it.ExitCode, it.Token, it.Routed = -1, "failure", true
		}

		if !it.Routed {
			it.Next = every
			if observe != nil {
				observe(it)
			}
			e.sleep(every)
			continue
		}
		if observe != nil {
			observe(it)
		}

		if it.Token == "failure" {
			return e.completeWait(dir, runID, stepID, "failure", nil, firstNonEmpty(stderr, stdout))
		}
		result, perr := emit.Parse(stdout, exitCode, step, e.Workflow.State)
		if perr != nil {
			// A routed token with an unintelligible payload is an authoring
			// bug, routed rather than thrown for finding I3's reason: a
			// thrown error here would leave the run parked forever with no
			// TRANSITION and no way out but pawl abandon.
			return e.completeWait(dir, runID, stepID, "failure", nil, perr.Error())
		}
		return e.completeWait(dir, runID, stepID, result.Outcome, result.Writes, "")
	}
}

// pollPrecheck re-reads the run from disk and answers the one question the
// loop asks over and over: is this run still parked at this exact wait step?
// Everything that means "no" — the run directory gone, the run finished, the
// cursor moved on — is ErrPollNotCurrent, the quiet early exit DESIGN.md §3
// asks for. A cursor that *is* on this step but whose kind is not wait is a
// different thing entirely: a caller mistake, and a hard error.
//
// It returns the step and the wall-clock time the run parked here: the
// timestamp of the last STEP_ENTER (or RESUME) recorded for this step.
func (e *Engine) pollPrecheck(dir, runID, stepID string) (*spec.Step, time.Time, error) {
	events, err := journal.ReadEvents(dir)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: run %q has no readable run directory (abandoned, or already cleaned up)", ErrPollNotCurrent, runID)
	}
	rs, err := journal.Replay(events)
	if err != nil {
		return nil, time.Time{}, err
	}
	// Any RUN_END ends the poller's business with this run — including
	// status "blocked", which is *not* terminal (DESIGN.md §4: "BLOCKED is
	// paused, not terminal"). That distinction matters here more than
	// anywhere else: a wait step that routes an outcome to blocked leaves
	// Replay's blocked-resume cursor back on that very step, so checking only
	// Terminal() would let a second pawl poll re-run the whole wait and
	// journal a second transition and RUN_END for a run that is paused
	// awaiting a person. A BLOCKED run resumes through pawl run's
	// RESUME{intervention: true} and nothing else.
	if rs.Ended {
		return nil, time.Time{}, fmt.Errorf("%w: run %q ended %s", ErrPollNotCurrent, runID, rs.EndStatus)
	}
	if rs.Cursor.Step != stepID {
		return nil, time.Time{}, fmt.Errorf("%w: the run is waiting at %q, not %q", ErrPollNotCurrent, rs.Cursor.Step, stepID)
	}
	step := e.Workflow.StepByID(stepID)
	if step == nil || step.Kind != "wait" {
		// Polling a step that exists and is current, but isn't a wait step,
		// is the same refusal class as a submit for the wrong step/kind
		// (ErrRefused, exit 4) — a caller mistake naming a real step that
		// simply isn't polled, not the ErrPollNotCurrent race above (the run
		// having moved on entirely) and not a broken run directory.
		return nil, time.Time{}, fmt.Errorf("%w: step %q is not a wait step; only a wait step is polled (DESIGN.md §3)", ErrRefused, stepID)
	}
	parkedAt := time.Time{}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Step == stepID && (ev.Kind == journal.KindStepEnter || ev.Kind == journal.KindResume) {
			parkedAt = ev.Time
			break
		}
	}
	if parkedAt.IsZero() {
		return nil, time.Time{}, fmt.Errorf("engine: run %q: no STEP_ENTER recorded for parked step %q", runID, stepID)
	}
	return step, parkedAt, nil
}

// execPoll runs one iteration of step's poll:, with ${key} substitution and
// DESIGN.md §9's script-path resolution — the same renderer run: goes through
// (resolveScriptPathTemplate), so a poll: script path resolves relative to
// the workflow file exactly as a run: one does. The captured output is
// persisted alongside every other step attempt's (finding I2), suffixed by
// iteration so one iteration does not erase the evidence of the last.
func (e *Engine) execPoll(step *spec.Step, dir, runID string, n int) (stdout, stderr string, exitCode int, err error) {
	rs, err := replayDir(dir)
	if err != nil {
		return "", "", -1, err
	}
	vals := buildValues(e.Workflow, rs, runID, step.ID, rs.Cursor.Attempt, rs.Visits[step.ID])
	cmd, err := resolveScriptPathTemplate(step.Poll, vals, filepath.Dir(e.Workflow.Path))
	if err != nil {
		return "", "", -1, fmt.Errorf("engine: rendering poll: for step %q: %w", step.ID, err)
	}
	stdout, stderr, exitCode, err = e.execShell(cmd, render.Keys(step.Poll), vals)
	e.writeStepOutput(dir, step.ID+".poll", n, stdout, stderr)
	return stdout, stderr, exitCode, err
}

// completeWait is pawl poll "doing what pawl submit would do internally"
// (DESIGN.md §3): under the run lock, and only after re-confirming the run is
// still parked here, it journals the routed line's writes, evaluates the
// step's postcondition if it declares one, takes the transition, and lets the
// ordinary run loop carry on through whatever deterministic steps follow —
// returning the same DISPATCH/ASK/WAIT/TERMINAL instruction any other
// advance would. Nothing about the transition machinery is duplicated here:
// routeReserved and runFrom are the very functions the deterministic and
// agentic paths call.
//
// failureText, set only on the hard-failure paths (a non-zero exit, the
// wall-clock ceiling, an unparseable payload), is journalled as a
// POSTCONDITION-kind diagnostic first so the run's last_error pseudo-key is
// populated for the blocked terminal that follows — the same reason
// advanceDeterministic journals one (finding I1).
func (e *Engine) completeWait(dir, runID, stepID, outcome string, writes map[string]any, failureText string) (Instruction, error) {
	lock, err := journal.AcquireLock(dir, false)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	// The loop held no lock, so re-confirm under it: another process may have
	// advanced this run between the routed iteration and this moment.
	step, _, err := e.pollPrecheck(dir, runID, stepID)
	if err != nil {
		return nil, err
	}

	log, err := journal.OpenLog(dir)
	if err != nil {
		return nil, err
	}
	defer log.Close()

	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	attempt := rs.Cursor.Attempt
	key := rs.Cursor.AttemptKey

	if failureText != "" {
		if err := e.journalFailureDiagnostic(log, runID, stepID, attempt, failureText); err != nil {
			return nil, err
		}
	}

	if len(writes) > 0 {
		if _, err := log.Append(journal.Event{
			Kind: journal.KindWrites, RunID: runID, Step: stepID,
			Attempt: attempt, Writes: writes,
		}); err != nil {
			return nil, err
		}
		rs, err = replayDir(dir)
		if err != nil {
			return nil, err
		}
	}

	// A postcondition is optional on a wait step (design/format-spec.md §B.7)
	// but legal, and it is evaluated here for the same reason it is on every
	// other kind: the actor that produced the result never judges it
	// (DESIGN.md §5). A failure routes the reserved failure outcome rather
	// than retrying: an attempts: retry of a wait step would mean re-parking
	// and re-polling, which is what a fresh `pawl run`/`pawl poll` already
	// does, and inventing a second, implicit re-park here would burn the
	// step's max_visits: from inside a command that is meant to be idempotent.
	if step.Postcondition != nil {
		vals := buildValues(e.Workflow, rs, runID, stepID, attempt, rs.Visits[stepID])
		cr, cerr := e.evaluatePostcondition(step, combinedRaw(e.Workflow, rs), vals)
		if cerr != nil {
			return nil, cerr
		}
		if _, err := log.Append(journal.Event{
			Kind: journal.KindPostcondition, RunID: runID, Step: stepID,
			Attempt: attempt, OK: cr.OK, Text: cr.Text, Soft: cr.Soft, AttemptKey: key,
		}); err != nil {
			return nil, err
		}
		if !cr.OK {
			outcome = "failure"
		}
	}

	instr, next, err := e.routeReserved(dir, log, runID, step, outcome, attempt)
	if err != nil {
		return nil, err
	}
	if next != nil {
		return e.runFrom(dir, log, runID, *next)
	}
	return instr, nil
}

// firstNonEmpty returns the first of values that is non-empty once trimmed —
// stderr, then stdout, for a failed poll:'s diagnostic text, exactly as
// advanceDeterministic picks it.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
