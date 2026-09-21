package engine

import (
	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// parkWait enters a kind: wait step and hands back the Wait instruction the
// caller prints as DESIGN.md §2's WAIT line.
//
// It is deliberately the thinnest of the three dispatchers. A wait step's
// body is its poll: command, and the engine never runs it here — not once.
// DESIGN.md §3 puts the whole polling loop in `pawl poll`, which the session
// runs under Monitor precisely because `pawl run` cannot block for hours,
// and design/format-spec.md §13 makes the division explicit: the poller
// re-runs poll: every every:, and the *first iteration* carrying a routed
// token ends the loop. There is no "iteration zero" belonging to `pawl run`
// — a speculative poll here would be an unrequested first iteration whose
// routed token `pawl run` has no machinery to submit.
//
// reentry distinguishes the two ways the cursor arrives at a wait step:
//
//   - reentry == false: the run loop just transitioned here (or the run
//     started here). This is a real visit — the cap is checked and a
//     STEP_ENTER is journaled, exactly as dispatchAgentic does, and that
//     STEP_ENTER-with-no-TRANSITION *is* the record of "parked here": Replay
//     puts the cursor on this step and the run stays live. No ninth event
//     kind is invented for it (DESIGN.md §4 names exactly eight); what
//     distinguishes "waiting for pawl poll" from "waiting for pawl submit"
//     is the parked step's kind:, which plan.json pins for the run.
//   - reentry == true: a second `pawl run` on a run already parked here
//     (Engine.Resume). The engine did no work while parked, so re-parking is
//     an idempotent re-print of the same WAIT line — no second STEP_ENTER,
//     which would otherwise burn a max_visits: entry per re-run and
//     eventually exhaust a step that never did anything. DESIGN.md §3: "on
//     resume the model simply runs pawl poll again — a wait asks about the
//     present state of the world."
func (e *Engine) parkWait(dir string, log *journal.Log, runID string, step *spec.Step, cur journal.Cursor, reentry bool) (Instruction, error) {
	if reentry {
		return waitInstruction(runID, step), nil
	}

	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	if e.checkCaps(rs, step) {
		instr, next, err := e.routeReserved(dir, log, runID, step, "exhausted", cur.Attempt)
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
	return waitInstruction(runID, step), nil
}

// waitInstruction builds the Wait value for step. every: is already
// defaulted by spec's loader (spec.DefaultEvery), and timeout: is required
// on a wait step by the validator, so both are simply carried through.
func waitInstruction(runID string, step *spec.Step) Wait {
	return Wait{RunID: runID, Step: step.ID, Every: step.Every, Timeout: step.Timeout}
}
