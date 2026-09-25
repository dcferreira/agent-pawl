package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/render"
)

// evaluateInvariantCheck runs inv's check: exactly the way a postcondition's
// command: is evaluated (evaluateCommandPostcondition): the same
// render/scriptpath/exec path (resolveScriptPathTemplate + execShell), so
// the same injection-safety reasoning applies — a state value can never
// reach a live shell context unquoted (see scriptpath.go's own doc
// comment). Unlike a postcondition, every way the check could fail to even
// run — an unresolvable ${key}, a missing script, a failed exec, the
// wall-clock ceiling — counts as violated here rather than escaping as a Go
// error: design/format-spec.md §10/§B.10 and docs/guards-and-invariants.md
// are explicit that a check that "cannot run at all" is violated, not an
// engine failure, since an invariant is meant to be a safety net and a net
// that can itself throw defeats the purpose.
func (e *Engine) evaluateInvariantCheck(tmpl string, vals render.Values) (holds bool, diagnostic string) {
	cmd, err := resolveScriptPathTemplate(tmpl, vals, filepath.Dir(e.Workflow.Path))
	if err != nil {
		return false, fmt.Sprintf("could not render check: %v", err)
	}
	out, errOut, exit, err := e.execShell(cmd, render.Keys(tmpl), vals)
	if err != nil {
		if errors.Is(err, errTimeout) {
			return false, "invariant check exceeded the wall-clock ceiling"
		}
		return false, fmt.Sprintf("could not run check: %v", err)
	}
	if exit == 0 {
		return true, ""
	}
	text := strings.TrimSpace(out)
	if text == "" {
		text = strings.TrimSpace(errOut)
	}
	return false, text
}

// preTransitionInvariantBlock evaluates every declared invariant, in
// declaration order, against the run's current journaled state, stopping at
// the first violated one (design/format-spec.md §10: "Invariants are
// evaluated by the engine after every step completion and after every pawl
// submit"). Every caller invokes it after the completing step's own
// WRITES/POSTCONDITION events are journaled and its outcome resolved, but
// strictly BEFORE that step's TRANSITION is appended — so a violation
// pre-empts the transition entirely, including one that would have routed
// straight to a terminal like done (design/format-spec.md §B.12).
//
// cursorStep is the step id RUN_END's own Step field carries, and therefore
// what journal.Replay's blocked-resume cursor lands back on (see
// journal.Replay's blocked-resume case, keyed only off RUN_END.Step and
// rs.Attempts — no TRANSITION need ever have been appended for it to work).
// For a kind: parallel step, that step id is the owning parallel step
// itself, never a branch: routeParallel calls this function exactly once,
// after every branch has already transitioned and the group's own outcome
// is resolved — not once per branch (see routeParallel's own doc comment
// for why: a blocked run mid-group would otherwise let an already-dispatched
// sibling branch's own submit land after the block, which fix-forward
// crash-resume assumes never happens). Because the check happens at the
// join, every branch has already transitioned by the time a violation here
// journals RUN_END{blocked} for cursorStep, leaving RunState.PendingBranches
// empty — resumeParallel's pending-branch re-derivation is therefore *not*
// what a blocked resume needs (it would just re-block on the exact same
// state forever); Resume's "parallel" case instead routes a blocked-run
// intervention to dispatchParallel, which re-enters the whole group, exactly
// as every other step kind re-runs wholesale on a blocked resume. This works
// because, at the point every caller invokes this function
// (including routeParallel), journal.RunState.Cursor is still parked on
// cursorStep (STEP_ENTER already replayed, no TRANSITION yet) — true
// uniformly for an ordinary step and for a parallel step's own group join
// alike.
//
// It returns a non-nil Instruction only when an invariant was violated: the
// blocked Terminal for the caller to return immediately instead of
// appending the TRANSITION or routing any further.
func (e *Engine) preTransitionInvariantBlock(dir string, log *journal.Log, runID, cursorStep string) (Instruction, error) {
	if len(e.Workflow.Invariants) == 0 {
		return nil, nil
	}
	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	vals := buildValues(e.Workflow, rs, runID, cursorStep, rs.Cursor.Attempt, rs.Visits[cursorStep])

	for _, inv := range e.Workflow.Invariants {
		holds, diag := e.evaluateInvariantCheck(inv.Check, vals)
		if holds {
			continue
		}

		reason := fmt.Sprintf("invariant %q violated: %s", inv.ID, inv.Message)
		if diag != "" && len(diag) <= 200 {
			reason = fmt.Sprintf("%s (%s)", reason, diag)
		}

		if _, err := log.Append(journal.Event{
			Kind: journal.KindRunEnd, RunID: runID, Step: cursorStep,
			Status: "blocked", Reason: reason,
		}); err != nil {
			return nil, err
		}

		rs2, err := replayDir(dir)
		if err != nil {
			return nil, err
		}
		msgTmpl := ""
		if t, ok := e.Workflow.Terminal["blocked"]; ok {
			msgTmpl = t.Message
		}
		message := e.renderBlockedMessage(rs2, runID, cursorStep, cursorStep, msgTmpl)
		return Terminal{
			RunID: runID, Status: "blocked", StepID: cursorStep,
			Outcome: "invariant", Message: message,
		}, nil
	}
	return nil, nil
}
