package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/emit"
	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// Submit applies the result of an agentic step's dispatch: it refuses any
// (run, step, attempt) triple other than the one the journal says the
// engine is waiting on, naming what it is waiting on instead; validates the
// result against the step's writes: schema; journals the writes;
// evaluates the postcondition; resolves the outcome (success routes on,
// failure feeds the same attempt-retry machinery a deterministic
// postcondition failure does); and continues the run loop, returning the
// next instruction.
func (e *Engine) Submit(runID, stepID string, attempt int, result json.RawMessage) (Instruction, error) {
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
	step := e.Workflow.StepByID(stepID)
	if step == nil {
		return nil, fmt.Errorf("engine: step %q is not an agentic step awaiting submission", stepID)
	}

	// branchOf is set when stepID names a still-outstanding branch of the
	// kind: parallel step the run's cursor is actually parked on — the
	// second acceptance path Submit understands, alongside the ordinary
	// "cursor is parked directly on this agentic step" path. The cursor
	// itself never moves onto a branch (journal.Replay parks it on the
	// parallel step throughout — see RunState.PendingBranches), so a
	// branch submission is recognised by membership in
	// rs.PendingBranches[rs.Cursor.Step], not by matching the cursor.
	var branchOf string
	switch {
	case rs.Cursor.Step == stepID && rs.Cursor.Attempt == attempt:
		if step.Kind != "agentic" {
			return nil, fmt.Errorf("engine: step %q is not an agentic step awaiting submission", stepID)
		}
	case rs.PendingBranches[rs.Cursor.Step][stepID]:
		parallelStep := e.Workflow.StepByID(rs.Cursor.Step)
		if parallelStep == nil || parallelStep.Kind != "parallel" || step.Kind != "agentic" {
			return nil, fmt.Errorf("engine: refusing submit for %s/attempt %d: the run is waiting on %s/attempt %d",
				stepID, attempt, rs.Cursor.Step, rs.Cursor.Attempt)
		}
		branchOf = rs.Cursor.Step
	default:
		return nil, fmt.Errorf("engine: refusing submit for %s/attempt %d: the run is waiting on %s/attempt %d",
			stepID, attempt, rs.Cursor.Step, rs.Cursor.Attempt)
	}
	key := rs.Cursor.AttemptKey

	var parsed map[string]any
	var cr checkResult
	if len(bytes.TrimSpace(result)) == 0 {
		cr = checkResult{OK: false, Text: fmt.Sprintf("step %q: submitted result is empty", stepID)}
	} else if err := json.Unmarshal(result, &parsed); err != nil {
		cr = checkResult{OK: false, Text: fmt.Sprintf("step %q: submitted result is not a JSON object: %v", stepID, err)}
	} else {
		writes, schemaErr := validateAgenticWrites(step, parsed)
		if schemaErr != "" {
			cr = checkResult{OK: false, Text: schemaErr}
		} else {
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
			vals := buildValues(e.Workflow, rs, runID, stepID, attempt, rs.Visits[stepID])
			cr, err = e.evaluatePostcondition(step, combinedRaw(e.Workflow, rs), vals)
			if err != nil {
				return nil, err
			}
		}
	}

	if branchOf != "" {
		return e.submitBranch(dir, log, runID, branchOf, step, attempt, cr)
	}

	if cr.OK {
		if step.Postcondition != nil {
			if _, err := log.Append(journal.Event{
				Kind: journal.KindPostcondition, RunID: runID, Step: stepID,
				Attempt: attempt, OK: true, Soft: cr.Soft, AttemptKey: key,
			}); err != nil {
				return nil, err
			}
		}
		return e.routeAgentic(dir, log, runID, step, "success", attempt)
	}

	nextKey := key
	if step.AttemptKey == "" {
		nextKey = hashFailureText(cr.Text)
	}
	if _, err := log.Append(journal.Event{
		Kind: journal.KindPostcondition, RunID: runID, Step: stepID, Attempt: attempt,
		OK: false, Text: cr.Text, Soft: cr.Soft, AttemptKey: nextKey,
	}); err != nil {
		return nil, err
	}
	rs, err = replayDir(dir)
	if err != nil {
		return nil, err
	}

	// C1: re-check the visit/step caps before redispatching a retry, not
	// only when the step was first entered — see advanceDeterministic for
	// why this is a symmetrical, defensive no-op given Retry-aware Visits
	// accounting (a retry never advances Visits), applied here for the same
	// reason the reviewer asked for it on the deterministic path.
	if e.checkCaps(rs, step) {
		return e.routeAgentic(dir, log, runID, step, "exhausted", attempt)
	}

	nextAttempt := nextTryNumber(attempt, rs.Attempts[journal.AttemptRef{Step: stepID, Key: nextKey}])
	if nextAttempt > step.Attempts {
		return e.routeAgentic(dir, log, runID, step, "failure", attempt)
	}

	if _, err := log.Append(journal.Event{
		Kind: journal.KindStepEnter, RunID: runID, Step: stepID,
		Attempt: nextAttempt, AttemptKey: nextKey, Retry: true,
	}); err != nil {
		return nil, err
	}
	rs, err = replayDir(dir)
	if err != nil {
		return nil, err
	}
	instr, derr := e.dispatchInstruction(dir, rs, step, runID, nextAttempt, false)
	if derr != nil {
		if !errors.Is(derr, errContextUnavailable) {
			return nil, derr
		}
		// N2: see agentic.go's dispatchAgentic for why this must be routed,
		// not thrown.
		if jerr := e.journalFailureDiagnostic(log, runID, stepID, nextAttempt, derr.Error()); jerr != nil {
			return nil, jerr
		}
		return e.routeAgentic(dir, log, runID, step, "failure", nextAttempt)
	}
	return instr, nil
}

// submitBranch journals an agentic branch's resolving report: its
// postcondition-if-declared (reusing the same OK/not-OK POSTCONDITION shape
// Submit's ordinary path journals, minus AttemptKey — a branch carries no
// attempt budget of its own) and its synthetic grouped TRANSITION (the
// "(joined)" marker; see journalBranchTransition). A branch never retries:
// cr.OK false here means the branch itself resolved to outcome "failure",
// full stop — not a redispatch.
//
// Once journaled, it checks whether any sibling branch is still pending
// (this package's all-or-nothing rule: a dispatched agentic branch can never
// be un-dispatched, so nothing here fails fast on the first failing branch —
// see routeParallel). If so, it returns BranchRecorded rather than acting
// further; once every branch has reported, it calls routeParallel to resolve
// the group and continue the run.
func (e *Engine) submitBranch(dir string, log *journal.Log, runID, parallelID string, step *spec.Step, attempt int, cr checkResult) (Instruction, error) {
	outcome := "success"
	if cr.OK {
		if step.Postcondition != nil {
			if _, err := log.Append(journal.Event{
				Kind: journal.KindPostcondition, RunID: runID, Step: step.ID,
				Attempt: attempt, OK: true, Soft: cr.Soft,
			}); err != nil {
				return nil, err
			}
		}
	} else {
		outcome = "failure"
		if _, err := log.Append(journal.Event{
			Kind: journal.KindPostcondition, RunID: runID, Step: step.ID, Attempt: attempt,
			OK: false, Text: cr.Text, Soft: cr.Soft,
		}); err != nil {
			return nil, err
		}
	}
	if err := journalBranchTransition(log, runID, parallelID, step.ID, attempt, outcome); err != nil {
		return nil, err
	}

	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	if pending := rs.PendingBranches[parallelID]; len(pending) > 0 {
		return BranchRecorded{
			RunID: runID, ParallelStep: parallelID, BranchStep: step.ID,
			Remaining: sortedKeys(pending),
		}, nil
	}
	parallelStep := e.Workflow.StepByID(parallelID)
	if parallelStep == nil {
		return nil, fmt.Errorf("engine: internal error: parallel step %q not found", parallelID)
	}
	return e.routeParallel(dir, log, runID, parallelStep, rs.Cursor.Attempt)
}

// validateAgenticWrites validates parsed against step's writes: schema
// (design/format-spec.md §B.6). A key not in the schema is discarded, not an
// error ("prose outside the schema is journalled and discarded" — §B.6);
// a key that is in the schema but does not fit its declared type is a hard
// validation error. A step with no typed writes: schema at all is treated
// as the empty schema and fails closed: no returned key is ever accepted
// (this should be unreachable once pawl run gates on spec.Validate, which
// requires a typed writes: on every agentic step).
func validateAgenticWrites(step *spec.Step, parsed map[string]any) (writes map[string]any, errText string) {
	if !step.Writes.IsTyped || len(step.Writes.Types) == 0 {
		if len(parsed) > 0 {
			return nil, fmt.Sprintf("step %q: agentic step has no writes: schema; refusing to accept any returned key (add a typed writes: map)", step.ID)
		}
		return map[string]any{}, ""
	}
	writes = map[string]any{}
	for k, declType := range step.Writes.Types {
		v, present := parsed[k]
		if !present {
			continue
		}
		coerced, err := coerceAgenticValue(k, v, declType)
		if err != nil {
			return nil, fmt.Sprintf("step %q: %s", step.ID, err.Error())
		}
		writes[k] = coerced
	}
	return writes, ""
}

func coerceAgenticValue(key string, v any, declType string) (any, error) {
	badType := func() error {
		return fmt.Errorf("key %q: value %v does not fit declared type %q", key, v, declType)
	}
	switch declType {
	case "integer":
		switch x := v.(type) {
		case float64:
			return int64(x), nil
		case string:
			i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			if err != nil {
				return nil, badType()
			}
			return i, nil
		default:
			return nil, badType()
		}
	case "number":
		switch x := v.(type) {
		case float64:
			return x, nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return nil, badType()
			}
			return f, nil
		default:
			return nil, badType()
		}
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return nil, badType()
		}
		return b, nil
	case "string":
		switch x := v.(type) {
		case string:
			return emit.EscapeC0(x), nil
		case float64:
			return emit.EscapeC0(fmt.Sprint(x)), nil
		default:
			return nil, badType()
		}
	case "json":
		return v, nil
	default:
		return nil, fmt.Errorf("key %q: declares unknown state type %q", key, declType)
	}
}
