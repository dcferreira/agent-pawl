package engine

import (
	"errors"
	"fmt"
	"sort"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// joinedTarget is the synthetic Target stamped on a branch's own TRANSITION:
// a branch step has no real transition target of its own (task 1 forbids
// next:/outcomes: on one), so this exists purely for journal/audit
// legibility. The actual routing happens later, at routeParallel, using the
// owning parallel step's own next:/outcomes:/catch:.
const joinedTarget = "(joined)"

// dispatchParallel enters a kind: parallel step: the cap check and the
// parallel step's own (ungrouped) STEP_ENTER reuse the exact same
// checkCaps/beginAttempt machinery any other step uses — a parallel step's
// own attempts:/max_visits: govern the whole group; branches carry no
// independent budget (task 1's validation). It then journals a grouped
// STEP_ENTER for each declared branch, in order: a deterministic branch
// executes immediately in-process (execBranchDeterministic); an agentic
// branch is rendered into a Dispatch and collected, its TRANSITION left
// pending until Submit reports it. If every branch turned out deterministic,
// the group resolves entirely here and dispatchParallel calls routeParallel
// itself, exactly mirroring how the run loop already batches consecutive
// deterministic steps without stopping; otherwise it returns DispatchParallel
// for the caller to fan the agentic branches out.
func (e *Engine) dispatchParallel(dir string, log *journal.Log, runID string, step *spec.Step, cur journal.Cursor, interrupted bool) (Instruction, error) {
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

	agentic, err := e.enterAndRunBranches(dir, log, runID, step, interrupted)
	if err != nil {
		return nil, err
	}
	if len(agentic) == 0 {
		return e.routeParallel(dir, log, runID, step, attempt)
	}
	return DispatchParallel{RunID: runID, Step: step.ID, Attempt: attempt, Agentic: agentic}, nil
}

// enterAndRunBranches journals a grouped STEP_ENTER for every one of step's
// declared branches, in order, and either executes it immediately
// (deterministic) or renders and returns its Dispatch (agentic, collected
// for the caller — its TRANSITION is left pending until Submit).
func (e *Engine) enterAndRunBranches(dir string, log *journal.Log, runID string, step *spec.Step, interrupted bool) ([]Dispatch, error) {
	var agentic []Dispatch
	for _, branchID := range step.Branches {
		branch := e.Workflow.StepByID(branchID)
		if branch == nil {
			return nil, fmt.Errorf("engine: internal error: parallel step %q: branch %q not found", step.ID, branchID)
		}
		if _, err := log.Append(journal.Event{
			Kind: journal.KindStepEnter, RunID: runID, Step: branch.ID,
			Attempt: 1, Group: step.ID,
		}); err != nil {
			return nil, err
		}
		d, dispatched, err := e.runOneBranch(dir, log, runID, step.ID, branch, interrupted)
		if err != nil {
			return nil, err
		}
		if dispatched {
			agentic = append(agentic, d)
		}
	}
	return agentic, nil
}

// runOneBranch runs (deterministic) or renders (agentic) one already-entered
// branch. For a deterministic branch it returns ok=false (nothing to
// dispatch: the branch already resolved, in-process). For an agentic branch
// it returns the Dispatch to hand to the caller, ok=true — unless gathering
// its context: fails, in which case (mirroring dispatchAgentic's own
// errContextUnavailable handling) the branch resolves as an immediate
// failure right here and ok is false.
func (e *Engine) runOneBranch(dir string, log *journal.Log, runID, parallelID string, branch *spec.Step, interrupted bool) (Dispatch, bool, error) {
	switch branch.Kind {
	case "deterministic":
		return Dispatch{}, false, e.execBranchDeterministic(dir, log, runID, parallelID, branch)
	case "agentic":
		rs, err := replayDir(dir)
		if err != nil {
			return Dispatch{}, false, err
		}
		instr, derr := e.dispatchInstruction(dir, rs, branch, runID, 1, interrupted)
		if derr != nil {
			if !errors.Is(derr, errContextUnavailable) {
				return Dispatch{}, false, derr
			}
			// N2: see dispatchAgentic/Submit for why a missing/failing
			// context: entry is routed, not thrown — here that means
			// resolving this one branch as failed, not the whole group.
			if jerr := e.journalFailureDiagnostic(log, runID, branch.ID, 1, derr.Error()); jerr != nil {
				return Dispatch{}, false, jerr
			}
			if jerr := journalBranchTransition(log, runID, parallelID, branch.ID, 1, "failure"); jerr != nil {
				return Dispatch{}, false, jerr
			}
			return Dispatch{}, false, nil
		}
		return instr.(Dispatch), true, nil
	default:
		return Dispatch{}, false, fmt.Errorf("engine: internal error: parallel step %q: branch %q has unsupported kind %q", parallelID, branch.ID, branch.Kind)
	}
}

// execBranchDeterministic runs a deterministic branch's body to completion
// in one, no-retry pass (a branch owns no attempts: budget of its own —
// task 1's validation forbids declaring one), reusing the exact same
// exec/journal/evaluate machinery advanceDeterministic's own loop body uses
// (runDeterministicAttempt), then journals the branch's synthetic TRANSITION
// (design's "(joined)" marker) recording whether it passed.
func (e *Engine) execBranchDeterministic(dir string, log *journal.Log, runID, parallelID string, branch *spec.Step) error {
	rs, err := replayDir(dir)
	if err != nil {
		return err
	}
	at, _, err := e.runDeterministicAttempt(dir, log, runID, branch, 1, rs, nil)
	if err != nil {
		return err
	}
	if at.hardFailed {
		return journalBranchTransition(log, runID, parallelID, branch.ID, 1, "failure")
	}
	if at.cr.OK {
		if branch.Postcondition != nil {
			if _, err := log.Append(journal.Event{
				Kind: journal.KindPostcondition, RunID: runID, Step: branch.ID,
				Attempt: 1, OK: true, Soft: at.cr.Soft,
			}); err != nil {
				return err
			}
		}
		return journalBranchTransition(log, runID, parallelID, branch.ID, 1, at.outcome)
	}
	if _, err := log.Append(journal.Event{
		Kind: journal.KindPostcondition, RunID: runID, Step: branch.ID, Attempt: 1,
		OK: false, Text: at.cr.Text, Soft: at.cr.Soft,
	}); err != nil {
		return err
	}
	return journalBranchTransition(log, runID, parallelID, branch.ID, 1, "failure")
}

// journalBranchTransition appends a branch's own grouped TRANSITION: Target
// is always the synthetic joinedTarget marker (a branch never routes
// anywhere itself), Outcome is "success" or "failure".
func journalBranchTransition(log *journal.Log, runID, parallelID, branchID string, attempt int, outcome string) error {
	_, err := log.Append(journal.Event{
		Kind: journal.KindTransition, RunID: runID, Step: branchID, Group: parallelID,
		Attempt: attempt, Target: joinedTarget, Outcome: outcome,
	})
	return err
}

// routeParallel resolves a parallel step's group outcome once every branch
// has transitioned (rs.PendingBranches[step.ID] is empty, all-or-nothing:
// nothing here ever fires while a dispatched agentic branch is still
// outstanding — DESIGN.md §5's fix-forward philosophy never abandons an
// unaccounted-for side effect): success iff every branch's resolved outcome
// was "success", otherwise failure. That group outcome is fed through the
// exact same resolveTarget/afterTransition machinery any other step's
// outcome goes through — the parallel step's own next:/outcomes:/catch:/
// attempts: govern what happens next, unchanged — and journaled as the
// parallel step's own (ungrouped) TRANSITION, moving rs.Cursor on exactly
// like any other step's transition.
func (e *Engine) routeParallel(dir string, log *journal.Log, runID string, step *spec.Step, attempt int) (Instruction, error) {
	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	outcome := "success"
	for _, b := range step.Branches {
		if rs.BranchOutcome[b] != "success" {
			outcome = "failure"
			break
		}
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

// resumeParallel re-derives, from rs.PendingBranches[step.ID] (already
// post-replay), which of step's branches are still outstanding after a
// crash or blocked-run intervention: an already-transitioned branch is
// never re-run or re-dispatched. A still-outstanding deterministic branch is
// safely re-exec'd inline (the same fix-forward caveat DESIGN.md §4 states
// generally for any interrupted step); a still-outstanding agentic branch is
// re-collected into a DispatchParallel{Interrupted: true}. If re-executing
// the deterministic stragglers leaves no agentic branch outstanding, the
// group resolves right here via routeParallel instead of returning an
// instruction with nothing to dispatch.
func (e *Engine) resumeParallel(dir string, log *journal.Log, runID string, step *spec.Step, rs *journal.RunState) (Instruction, error) {
	pending := rs.PendingBranches[step.ID]
	var agentic []Dispatch
	for _, branchID := range step.Branches {
		if !pending[branchID] {
			continue
		}
		branch := e.Workflow.StepByID(branchID)
		if branch == nil {
			return nil, fmt.Errorf("engine: internal error: parallel step %q: branch %q not found", step.ID, branchID)
		}
		d, dispatched, err := e.runOneBranch(dir, log, runID, step.ID, branch, true)
		if err != nil {
			return nil, err
		}
		if dispatched {
			agentic = append(agentic, d)
		}
	}
	if len(agentic) == 0 {
		return e.routeParallel(dir, log, runID, step, rs.Cursor.Attempt)
	}
	return DispatchParallel{RunID: runID, Step: step.ID, Attempt: rs.Cursor.Attempt, Agentic: agentic, Interrupted: true}, nil
}

// sortedKeys returns m's keys, sorted — used for BranchRecorded.Remaining.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
