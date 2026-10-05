package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	return e.submit(runID, stepID, attempt, nil, result)
}

// SubmitItem is Submit for one item of an agentic foreach: stepID is the
// foreach BODY step and item the zero-based index the Dispatch carried
// (pawl submit --item N). It is accepted only while the run's cursor is at
// the foreach step that owns stepID and that item is still pending; attempt
// is ignored (items are always attempt 1 — a body owns no attempts: budget).
func (e *Engine) SubmitItem(runID, stepID string, item, attempt int, result json.RawMessage) (Instruction, error) {
	return e.submit(runID, stepID, attempt, &item, result)
}

func (e *Engine) submit(runID, stepID string, attempt int, item *int, result json.RawMessage) (Instruction, error) {
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
	if step != nil && step.Kind == "wait" {
		// DESIGN.md §3, stated as a rule: "The model never runs pawl submit
		// for a wait result." design/format-spec.md §13 says why — pawl poll
		// submits on its own behalf, internally, so a model-issued submit
		// here would race the poller and route an outcome the poller never
		// observed. The generic "not an agentic step" refusal below would
		// also stop it, but it would leave the caller with no idea what to
		// run instead, which for the one command the /pawl skill is
		// explicitly told not to use is the whole point of the message.
		return nil, fmt.Errorf("%w: step %q is a wait step: its result is submitted by the poller, not by you — run `pawl poll --run %s --step %s` (DESIGN.md §3: the model never runs pawl submit for a wait result)", ErrRefused, stepID, runID, stepID)
	}
	if step == nil {
		return nil, fmt.Errorf("%w: step %q is not an agentic step awaiting submission", ErrRefused, stepID)
	}

	// A foreach body is submitted per item (--item N), never by step alone,
	// and --item is meaningless for any other step.
	var fe *foreachItem
	var foreachOwner *spec.Step
	for i := range e.Workflow.Steps {
		if f := e.Workflow.Steps[i].Foreach; f != nil && f.Body == stepID {
			foreachOwner = &e.Workflow.Steps[i]
			break
		}
	}
	switch {
	case foreachOwner == nil && item != nil:
		return nil, fmt.Errorf("%w: --item %d given, but step %q is not the body of a foreach step; --item applies only to a foreach body", ErrRefused, *item, stepID)
	case foreachOwner != nil:
		if step.Kind != "agentic" {
			return nil, fmt.Errorf("%w: step %q is the deterministic body of foreach step %q: the engine runs it per item itself and nothing is ever awaiting a submit for it", ErrRefused, stepID, foreachOwner.ID)
		}
		if item == nil {
			return nil, fmt.Errorf("%w: step %q is the body of foreach step %q; submit it per item with --item N (the index its DISPATCH block names)", ErrRefused, stepID, foreachOwner.ID)
		}
		if rs.Cursor.Step != foreachOwner.ID {
			return nil, fmt.Errorf("%w: submit for %s item %d, but the run is waiting on %s/attempt %d, not on foreach step %q",
				ErrRefused, stepID, *item, rs.Cursor.Step, rs.Cursor.Attempt, foreachOwner.ID)
		}
		n := len(rs.ForeachItems[foreachOwner.ID])
		if *item < 0 || *item >= n {
			return nil, fmt.Errorf("%w: foreach step %q has %d items (0..%d); item %d does not exist", ErrRefused, foreachOwner.ID, n, n-1, *item)
		}
		if !rs.PendingItems[foreachOwner.ID][*item] {
			return nil, fmt.Errorf("%w: item %d of foreach step %q is not awaiting a submit (already reported, or never dispatched); still pending: %s",
				ErrRefused, *item, foreachOwner.ID, pendingItemList(foreachOwner.Foreach.Body, rs.PendingItems[foreachOwner.ID]))
		}
		fe = &foreachItem{group: foreachOwner.ID, index: *item, item: rs.ForeachItems[foreachOwner.ID][*item]}
		attempt = 1
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
	case fe != nil:
		// validated above
	case rs.Cursor.Step == stepID && rs.Cursor.Attempt == attempt:
		if step.Kind != "agentic" {
			return nil, fmt.Errorf("%w: step %q is not an agentic step awaiting submission", ErrRefused, stepID)
		}
	case rs.PendingBranches[rs.Cursor.Step][stepID]:
		parallelStep := e.Workflow.StepByID(rs.Cursor.Step)
		if parallelStep == nil || parallelStep.Kind != "parallel" || step.Kind != "agentic" {
			return nil, fmt.Errorf("%w: submit for %s/attempt %d, but the run is waiting on %s/attempt %d",
				ErrRefused, stepID, attempt, rs.Cursor.Step, rs.Cursor.Attempt)
		}
		branchOf = rs.Cursor.Step
	default:
		return nil, fmt.Errorf("%w: submit for %s/attempt %d, but the run is waiting on %s/attempt %d",
			ErrRefused, stepID, attempt, rs.Cursor.Step, rs.Cursor.Attempt)
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
				wev := journal.Event{
					Kind: journal.KindWrites, RunID: runID, Step: stepID,
					Attempt: attempt, Writes: writes,
				}
				fe.stamp(&wev)
				if _, err := log.Append(wev); err != nil {
					return nil, err
				}
				rs, err = replayDir(dir)
				if err != nil {
					return nil, err
				}
			}
			vals := buildValues(e.Workflow, rs, runID, stepID, attempt, rs.Visits[stepID])
			fe.addPseudoKeys(vals)
			raw := combinedRaw(e.Workflow, rs)
			fe.overlayWrites(e.Workflow, rs, vals, raw)
			cr, err = e.evaluatePostcondition(step, raw, vals)
			if err != nil {
				return nil, err
			}
		}
	}

	if fe != nil {
		return e.submitItem(dir, log, runID, foreachOwner, step, fe, cr)
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
	instr, derr := e.dispatchInstruction(dir, rs, step, runID, nextAttempt, false, nil)
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

// pendingItemList renders the pending item indices as "body[i], body[j]".
func pendingItemList(body string, pending map[int]bool) string {
	idx := make([]int, 0, len(pending))
	for i := range pending {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	parts := make([]string, len(idx))
	for k, i := range idx {
		parts[k] = fmt.Sprintf("%s[%d]", body, i)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// submitItem journals an agentic foreach item's resolving report, as
// submitBranch does for a branch: its grouped POSTCONDITION (if the body
// declares one, or on failure) and its grouped TRANSITION. The item's WRITES
// were already journaled item-scoped by the caller. A body owns no attempts:
// budget, so a failed postcondition or an invalid return is just a failed
// item. While other items are pending it returns ItemRecorded; the last one
// to land runs joinForeach.
func (e *Engine) submitItem(dir string, log *journal.Log, runID string, foreachStep, body *spec.Step, fe *foreachItem, cr checkResult) (Instruction, error) {
	outcome := "success"
	var pc *journal.Event
	switch {
	case cr.OK && body.Postcondition != nil:
		pc = &journal.Event{Kind: journal.KindPostcondition, RunID: runID, Step: body.ID, Attempt: 1, OK: true, Soft: cr.Soft}
	case !cr.OK:
		outcome = "failure"
		pc = &journal.Event{Kind: journal.KindPostcondition, RunID: runID, Step: body.ID, Attempt: 1, OK: false, Text: cr.Text, Soft: cr.Soft}
	}
	if pc != nil {
		fe.stamp(pc)
		if _, err := log.Append(*pc); err != nil {
			return nil, err
		}
	}
	if err := journalItemTransition(log, runID, body.ID, 1, fe, outcome); err != nil {
		return nil, err
	}
	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	if pending := rs.PendingItems[foreachStep.ID]; len(pending) > 0 {
		rem := make([]int, 0, len(pending))
		for i := range pending {
			rem = append(rem, i)
		}
		sort.Ints(rem)
		return ItemRecorded{RunID: runID, ForeachStep: foreachStep.ID, BodyStep: body.ID, Item: fe.index, Remaining: rem}, nil
	}
	return e.joinForeach(dir, log, runID, foreachStep, rs.Cursor.Attempt, rs.ForeachItems[foreachStep.ID])
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
