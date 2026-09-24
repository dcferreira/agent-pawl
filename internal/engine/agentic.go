package engine

import (
	"errors"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// dispatchAgentic enters an agentic step (a cap check, then STEP_ENTER) and
// returns the Dispatch instruction the caller hands to the session
// (DESIGN.md §2, §3). Unlike a deterministic step, it does not loop: the
// result arrives later, out of process, via Submit.
func (e *Engine) dispatchAgentic(dir string, log *journal.Log, runID string, step *spec.Step, cur journal.Cursor, interrupted bool) (Instruction, error) {
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
	instr, derr := e.dispatchInstruction(dir, rs, step, runID, attempt, interrupted)
	if derr != nil {
		if !errors.Is(derr, errContextUnavailable) {
			return nil, derr
		}
		// N2: a missing/failing context: entry is an authoring bug
		// discovered only at dispatch time, exactly like I3's unintelligible
		// stdout — it must be routed, not thrown, or STEP_ENTER is left
		// dangling with no TRANSITION/RUN_END and Resume dies identically.
		if jerr := e.journalFailureDiagnostic(log, runID, step.ID, attempt, derr.Error()); jerr != nil {
			return nil, jerr
		}
		return e.routeAgentic(dir, log, runID, step, "failure", attempt)
	}
	return instr, nil
}

// routeAgentic journals the TRANSITION for outcome (success or failure) off
// an agentic step and continues the run: if the target is another step, it
// keeps running (deterministic steps in-process, or a further Dispatch);
// if it is a terminal, it returns the Terminal instruction.
func (e *Engine) routeAgentic(dir string, log *journal.Log, runID string, step *spec.Step, outcome string, attempt int) (Instruction, error) {
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
