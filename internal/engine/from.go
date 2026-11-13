package engine

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// ErrFromTarget marks a ResumeFrom refusal because the named step is not a
// legal --from target for this run: not a step at all, a terminal, a step
// owned by a parallel step (a branch or a foreach body), or a top-level step
// the run has never entered. It is the caller's input that is wrong, so the
// cli classes it as a usage error (exit 2), not a refusal.
var ErrFromTarget = errors.New("engine: --from target refused")

// ResumeFrom moves a live run (blocked, or running and parked at any step)
// to target and continues from there: `pawl run <name> --from <step>`.
//
// Legal targets are the run's current cursor step and every top-level step
// the run has already entered (rs.Visits > 0). That set is well defined in a
// cyclic graph, and it rules out a forward jump past the steps whose writes
// the target may read. Allowing unvisited targets later would be a
// loosening; taking them away would be a break.
//
// The move is journaled with event kinds, and replay semantics, that every
// 1.x binary already has, so an older 1.x binary resumes a moved run at the
// target exactly as this one does:
//
//  1. RESUME{step: cursor, attempt: 1, intervention: true}: the record a
//     blocked resume writes. It un-ends a blocked run (Replay honours a
//     RESUME after RUN_END{blocked} only) and is equally valid on a running
//     one: a person intervened at the current step.
//  2. TRANSITION{step: cursor, target: target, outcome: "", via_from: true}:
//     an ungrouped, non-catch edge, so Replay's cursor becomes {target, 1}.
//     Because (1) set the left step's postcondition flag to passed, the
//     §B.4 clearing rule drops the left step's attempt budget, exactly as a
//     blocked resume resets it. via_from is a marker field only; Replay does
//     not read it, and an older binary ignores it on decode.
//
// Then the normal run loop enters target, so its STEP_ENTER counts one
// visit and max_visits:/max_steps: are checked as on any arrival. Nothing is
// reset and nothing is rolled back: state is kept as it is (fix-forward).
// The left step's postcondition is not evaluated (that is the person's
// override), no outcome is resolved (so no catch: either), and invariants
// are not checked on the move itself (state did not change); every later
// transition checks them as usual.
//
// A move away from a kind: parallel step with branches or items still
// pending is refused (ErrRefused): their late submits would land on a
// visit the run has already left. Finish or submit them first.
//
// A crash between (1) and (2) leaves a run resumed at its old step; running
// --from again repeats the move cleanly.
func (e *Engine) ResumeFrom(runID, target string, force bool) (Instruction, error) {
	dir, release, log, rs, err := e.openForResume(runID, force)
	if err != nil {
		return nil, err
	}
	defer release()
	defer log.Close()

	if err := e.checkFromTarget(rs, target); err != nil {
		return nil, err
	}
	from := rs.Cursor.Step
	if from == "" {
		return nil, fmt.Errorf("engine: internal error: run %q has no cursor step to move from", runID)
	}
	if cur := e.Workflow.StepByID(from); cur != nil && cur.Kind == "parallel" {
		if pending := pendingMembers(rs, from); len(pending) > 0 {
			return nil, fmt.Errorf("%w: step %q is a parallel step with %s still pending; let the group finish first (submit the agentic ones; `pawl run` without --from resumes the rest), or `pawl abandon --run %s`", ErrRefused, from, strings.Join(pending, ", "), runID)
		}
	}

	if _, err := log.Append(journal.Event{
		Kind:         journal.KindResume,
		RunID:        runID,
		Step:         from,
		Attempt:      1,
		AttemptKey:   rs.Cursor.AttemptKey,
		Intervention: true,
	}); err != nil {
		return nil, err
	}
	if _, err := log.Append(journal.Event{
		Kind:    journal.KindTransition,
		RunID:   runID,
		Step:    from,
		Attempt: 1,
		Target:  target,
		ViaFrom: true,
	}); err != nil {
		return nil, err
	}
	return e.runFrom(dir, log, runID, journal.Cursor{Step: target, Attempt: 1})
}

// checkFromTarget reports (wrapping ErrFromTarget) why target is not a legal
// --from target for a run in state rs, or nil when it is.
func (e *Engine) checkFromTarget(rs *journal.RunState, target string) error {
	visited := visitedSteps(e, rs)
	legal := "legal targets for this run: (none)"
	if len(visited) > 0 {
		legal = "legal targets for this run: " + strings.Join(visited, ", ")
	}
	step := e.Workflow.StepByID(target)
	if step == nil {
		if e.Workflow.IsValidTarget(target) {
			return fmt.Errorf("%w: %q is a terminal, not a step; to end a run use `pawl abandon` (%s)", ErrFromTarget, target, legal)
		}
		return fmt.Errorf("%w: workflow %q has no step %q (%s)", ErrFromTarget, e.Workflow.Workflow, target, legal)
	}
	if owner, ok := ownedSteps(e)[target]; ok {
		return fmt.Errorf("%w: step %q belongs to parallel step %q; --from takes a top-level step (%s)", ErrFromTarget, target, owner, legal)
	}
	if target != rs.Cursor.Step && rs.Visits[target] == 0 {
		return fmt.Errorf("%w: step %q has not been entered by this run; --from takes the current step or a step the run has already entered (%s)", ErrFromTarget, target, legal)
	}
	return nil
}

// visitedSteps returns the legal --from targets for a run in state rs: the
// cursor step plus every top-level step with at least one recorded visit, in
// workflow declaration order.
func visitedSteps(e *Engine, rs *journal.RunState) []string {
	owned := ownedSteps(e)
	var out []string
	for _, s := range e.Workflow.Steps {
		if _, ok := owned[s.ID]; ok {
			continue
		}
		if s.ID == rs.Cursor.Step || rs.Visits[s.ID] > 0 {
			out = append(out, s.ID)
		}
	}
	return out
}

// ownedSteps maps each step owned by a parallel step (a branches: member or
// a foreach: body) to its owner's id.
func ownedSteps(e *Engine) map[string]string {
	owner := map[string]string{}
	for _, s := range e.Workflow.Steps {
		if s.Kind != "parallel" {
			continue
		}
		for _, b := range s.Branches {
			if _, ok := owner[b]; !ok {
				owner[b] = s.ID
			}
		}
		if s.Foreach != nil && s.Foreach.Body != "" {
			if _, ok := owner[s.Foreach.Body]; !ok {
				owner[s.Foreach.Body] = s.ID
			}
		}
	}
	return owner
}

// pendingMembers names, sorted, the branches and foreach items of parallel
// step id that have entered and not yet transitioned.
func pendingMembers(rs *journal.RunState, id string) []string {
	var out []string
	for b, pending := range rs.PendingBranches[id] {
		if pending {
			out = append(out, "branch "+b)
		}
	}
	var items []int
	for i, pending := range rs.PendingItems[id] {
		if pending {
			items = append(items, i)
		}
	}
	sort.Strings(out)
	sort.Ints(items)
	for _, i := range items {
		out = append(out, fmt.Sprintf("item %d", i))
	}
	return out
}
