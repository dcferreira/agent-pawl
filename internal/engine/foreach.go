package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// foreachItem is the per-item context a foreach body runs under: which
// foreach step owns it (group), the item's index into the frozen snapshot
// and its value. The zero *foreachItem (nil) means "an ordinary step", and
// every method is nil-safe so runDeterministicAttemptItem needs no branches
// of its own for the plain case.
type foreachItem struct {
	group string
	index int
	item  any
}

// addPseudoKeys adds ${item} and ${item_index} to vals. item_index is the
// zero-based integer; item is a JSON string's raw text, or any other JSON
// value's compact encoding. Both are plain string values, so every render
// context substitutes them exactly like any other ${key}: in shell
// contexts as ONE shell-quoted token (render.RenderShell), never re-parsed
// and never handed to "sh -c" (the validator's rule 28 rejects that
// spelling); and as PAWL_ITEM / PAWL_ITEM_INDEX in the env of any command
// whose template names them (render.EnvFor).
func (fe *foreachItem) addPseudoKeys(vals render.Values) {
	if fe == nil {
		return
	}
	vals["item_index"] = render.StringValue(strconv.Itoa(fe.index))
	if s, ok := fe.item.(string); ok {
		vals["item"] = render.StringValue(s)
		return
	}
	b, err := json.Marshal(fe.item)
	if err != nil {
		b = []byte("null")
	}
	vals["item"] = render.StringValue(string(b))
}

// logName is the name stdout/stderr logs are written under: per item, so
// one item's log never overwrites another's.
func (fe *foreachItem) logName(stepID string) string {
	if fe == nil {
		return stepID
	}
	return fmt.Sprintf("%s-item%d", stepID, fe.index)
}

// stamp marks ev as belonging to this item (Group + Item).
func (fe *foreachItem) stamp(ev *journal.Event) {
	if fe == nil {
		return
	}
	idx := fe.index
	ev.Group, ev.Item = fe.group, &idx
}

// overlayWrites layers this item's own writes (journal.RunState.ItemWrites,
// deliberately not in rs.State) over raw and vals, so the body's
// postcondition can see what the body just wrote.
func (fe *foreachItem) overlayWrites(w *spec.Workflow, rs *journal.RunState, vals render.Values, raw map[string]any) {
	if fe == nil {
		return
	}
	for k, v := range rs.ItemWrites[fe.group][fe.index] {
		raw[k] = v
		vals[k] = renderValue(declaredType(w, k), v)
	}
}

// journalAttemptDiagnostic is journalFailureDiagnostic, stamped with fe's
// Group/Item when fe is set.
func (e *Engine) journalAttemptDiagnostic(log *journal.Log, runID, stepID string, attempt int, text string, fe *foreachItem) error {
	ev := journal.Event{
		Kind: journal.KindPostcondition, RunID: runID, Step: stepID,
		Attempt: attempt, OK: false, Text: text,
	}
	fe.stamp(&ev)
	_, err := log.Append(ev)
	return err
}

// dispatchForeach enters a kind: parallel step that declares foreach:
// (dispatchParallel's counterpart for that form): the same checkCaps and
// beginAttempt, then the step's own ungrouped STEP_ENTER — carrying Items,
// the frozen snapshot of the list, so a resumed run iterates the list the
// first entry saw — and then every item, in order, executed in-process
// (a foreach body is always deterministic), before the join.
//
// A foreach: over a value that is not a JSON array resolves the step as
// failure (with a diagnostic naming the key); a list longer than max_items
// resolves it as exhausted; an empty list resolves it as success with an
// empty collect. None of these three journal Items (Event.Items is
// omitempty, so an empty snapshot would not survive anyway), so each is
// resolved here at entry, never recovered from replay.
func (e *Engine) dispatchForeach(dir string, log *journal.Log, runID string, step *spec.Step, cur journal.Cursor) (Instruction, error) {
	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	if e.checkCaps(rs, step) {
		return e.routeForeachReserved(dir, log, runID, step, "exhausted", cur.Attempt, false)
	}
	vals := buildValues(e.Workflow, rs, runID, step.ID, cur.Attempt, rs.Visits[step.ID])
	attempt, key, err := e.beginAttempt(rs, step, cur, vals)
	if err != nil {
		return nil, err
	}

	f := step.Foreach
	enter := journal.Event{
		Kind: journal.KindStepEnter, RunID: runID, Step: step.ID,
		Attempt: attempt, AttemptKey: key,
	}
	v := combinedRaw(e.Workflow, rs)[f.Over]
	items, isArray := v.([]any)
	switch {
	case !isArray:
		if _, err := log.Append(enter); err != nil {
			return nil, err
		}
		text := fmt.Sprintf("step %q: foreach.over %q must hold a JSON array, but it holds %s", step.ID, f.Over, describeJSONKind(v))
		if err := e.journalFailureDiagnostic(log, runID, step.ID, attempt, text); err != nil {
			return nil, err
		}
		if err := e.clearForeachCollect(log, runID, step, attempt); err != nil {
			return nil, err
		}
		return e.routeForeachReserved(dir, log, runID, step, "failure", attempt, true)
	case len(items) > f.MaxItemsOrDefault():
		if _, err := log.Append(enter); err != nil {
			return nil, err
		}
		text := fmt.Sprintf("step %q: foreach.over %q holds %d items, over the max_items cap of %d", step.ID, f.Over, len(items), f.MaxItemsOrDefault())
		if err := e.journalFailureDiagnostic(log, runID, step.ID, attempt, text); err != nil {
			return nil, err
		}
		if err := e.clearForeachCollect(log, runID, step, attempt); err != nil {
			return nil, err
		}
		return e.routeForeachReserved(dir, log, runID, step, "exhausted", attempt, true)
	case len(items) == 0:
		if _, err := log.Append(enter); err != nil {
			return nil, err
		}
		return e.joinForeach(dir, log, runID, step, attempt, nil)
	}
	enter.Items = items
	if _, err := log.Append(enter); err != nil {
		return nil, err
	}
	return e.runForeachItems(dir, log, runID, step, attempt, items, nil)
}

// clearForeachCollect journals an empty collect: for a foreach step that
// resolves at entry without running any item (a non-array over:, or a list
// over max_items), so a re-entered step never leaves the previous visit's
// per-item results in the collect key for its route to render.
func (e *Engine) clearForeachCollect(log *journal.Log, runID string, step *spec.Step, attempt int) error {
	_, err := log.Append(journal.Event{
		Kind: journal.KindWrites, RunID: runID, Step: step.ID, Attempt: attempt,
		Writes: map[string]any{step.Foreach.Collect: []any{}},
	})
	return err
}

// routeForeachReserved resolves step with a reserved outcome and continues
// the run loop (routeReserved plus runFrom, the Instruction-returning shape
// dispatchForeach and the join need).
func (e *Engine) routeForeachReserved(dir string, log *journal.Log, runID string, step *spec.Step, outcome string, attempt int, runInvariants bool) (Instruction, error) {
	instr, next, err := e.routeReserved(dir, log, runID, step, outcome, attempt, runInvariants)
	if err != nil {
		return nil, err
	}
	if next != nil {
		return e.runFrom(dir, log, runID, *next)
	}
	return instr, nil
}

// describeJSONKind names what v is, for the not-an-array diagnostic.
func describeJSONKind(v any) string {
	switch v.(type) {
	case nil:
		return "nothing (it is unset or null)"
	case map[string]any:
		return "an object"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	default:
		return "a number"
	}
}

// runForeachItems runs every item of items that is not already in done (an
// item with a journaled outcome, which a crash-resume never re-runs; nil at
// a fresh entry), in order, then joins.
func (e *Engine) runForeachItems(dir string, log *journal.Log, runID string, step *spec.Step, attempt int, items []any, done map[int]string) (Instruction, error) {
	body := e.Workflow.StepByID(step.Foreach.Body)
	if body == nil {
		return nil, fmt.Errorf("engine: internal error: foreach step %q: body %q not found", step.ID, step.Foreach.Body)
	}
	for i, item := range items {
		if _, ok := done[i]; ok {
			continue
		}
		if err := e.runForeachItem(dir, log, runID, step, body, &foreachItem{group: step.ID, index: i, item: item}); err != nil {
			return nil, err
		}
	}
	return e.joinForeach(dir, log, runID, step, attempt, items)
}

// runForeachItem runs one item of a foreach step to completion: a grouped
// STEP_ENTER, the body (runDeterministicAttemptItem, with the body's retry:
// honoured exactly as advanceDeterministic honours it — a hard failure is
// retried up to max_attempts, after which the item is a failure), its
// WRITES/POSTCONDITION (grouped, so per-item), and a grouped TRANSITION to
// the synthetic joinedTarget with outcome success or failure. A body owns no
// attempts: budget, so a failed postcondition is simply a failed item.
func (e *Engine) runForeachItem(dir string, log *journal.Log, runID string, step, body *spec.Step, fe *foreachItem) error {
	enter := func(hardRetry int) error {
		ev := journal.Event{
			Kind: journal.KindStepEnter, RunID: runID, Step: body.ID,
			Attempt: 1, HardRetry: hardRetry,
		}
		fe.stamp(&ev)
		_, err := log.Append(ev)
		return err
	}
	appendEv := func(ev journal.Event) error {
		fe.stamp(&ev)
		_, err := log.Append(ev)
		return err
	}
	if err := enter(0); err != nil {
		return err
	}
	rs, err := replayDir(dir)
	if err != nil {
		return err
	}
	// Frozen once, as advanceDeterministic does, so a retried try never
	// sees the diagnostic the previous try just journaled.
	lastError := rs.LastError

	at, _, err := e.runDeterministicAttemptItem(dir, log, runID, body, 1, rs, &lastError, fe)
	if err != nil {
		return err
	}
	hardRetry := 0
	for at.retryable && body.Retry != nil && hardRetry+1 < body.Retry.MaxAttempts {
		tryNumber := hardRetry + 1
		backoff, berr := time.ParseDuration(body.Retry.Backoff)
		if berr != nil {
			return fmt.Errorf("engine: step %q: retry.backoff: %q is not a duration (validator should have rejected this): %w", body.ID, body.Retry.Backoff, berr)
		}
		sleepFor := backoff * time.Duration(tryNumber)
		e.logRetry("pawl: step %q item %d hard-failed (try %d of %d); retrying in %s", body.ID, fe.index, tryNumber, body.Retry.MaxAttempts, sleepFor)
		hardRetry = tryNumber
		if err := enter(hardRetry); err != nil {
			return err
		}
		e.sleep(sleepFor)
		if rs, err = replayDir(dir); err != nil {
			return err
		}
		if at, _, err = e.runDeterministicAttemptItem(dir, log, runID, body, 1, rs, &lastError, fe); err != nil {
			return err
		}
	}

	outcome := "failure"
	switch {
	case at.hardFailed:
		// the attempt already journaled its own diagnostic
	case at.cr.OK && at.outcome == "success":
		outcome = "success"
		if body.Postcondition != nil || hardRetry > 0 {
			// A recovered retry must clear the earlier try's diagnostic from
			// ${last_error}, exactly as advanceDeterministic does.
			if err := appendEv(journal.Event{
				Kind: journal.KindPostcondition, RunID: runID, Step: body.ID,
				Attempt: 1, OK: true, Soft: at.cr.Soft,
			}); err != nil {
				return err
			}
		}
	case at.cr.OK:
		text := fmt.Sprintf("step %q: foreach body reported outcome %q; a body resolves only to success or failure", body.ID, at.outcome)
		if err := e.journalAttemptDiagnostic(log, runID, body.ID, 1, text, fe); err != nil {
			return err
		}
	default:
		if err := appendEv(journal.Event{
			Kind: journal.KindPostcondition, RunID: runID, Step: body.ID, Attempt: 1,
			OK: false, Text: at.cr.Text, Soft: at.cr.Soft,
		}); err != nil {
			return err
		}
	}
	return appendEv(journal.Event{
		Kind: journal.KindTransition, RunID: runID, Step: body.ID, Attempt: 1,
		Target: joinedTarget, Outcome: outcome,
	})
}

// joinForeach is routeParallel's counterpart for a foreach step, run once
// no item is pending: it builds the step's collect: value — a JSON array,
// in list order, of {index, item, outcome, writes, error} objects — and
// journals it as an ordinary UNGROUPED WRITES event on the step so it lands
// in global state (the only way an item's own writes reach it); resolves
// the three-way outcome (success iff every item succeeded — vacuously so
// for an empty list — failure iff none did, partial otherwise); then, with
// collect already written so an invariant can see it, evaluates invariants
// once, resolves the route and journals the step's own TRANSITION exactly
// as routeParallel does.
func (e *Engine) joinForeach(dir string, log *journal.Log, runID string, step *spec.Step, attempt int, items []any) (Instruction, error) {
	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	events, err := journal.ReadEvents(dir)
	if err != nil {
		return nil, err
	}
	collect := make([]any, 0, len(items))
	succeeded := 0
	for i, item := range items {
		outcome := rs.ItemOutcome[step.ID][i]
		if outcome != "success" {
			outcome = "failure"
		} else {
			succeeded++
		}
		writes := map[string]any{}
		for k, v := range rs.ItemWrites[step.ID][i] {
			writes[k] = v
		}
		var errText any
		if outcome == "failure" {
			errText = lastItemError(events, step.ID, i)
		}
		collect = append(collect, map[string]any{
			"index": i, "item": item, "outcome": outcome, "writes": writes, "error": errText,
		})
	}
	outcome := "partial"
	switch {
	case succeeded == len(items):
		outcome = "success"
	case succeeded == 0:
		outcome = "failure"
	}

	if _, err := log.Append(journal.Event{
		Kind: journal.KindWrites, RunID: runID, Step: step.ID, Attempt: attempt,
		Writes: map[string]any{step.Foreach.Collect: collect},
	}); err != nil {
		return nil, err
	}
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

// lastItemError returns the text of the latest failed POSTCONDITION
// (a postcondition failure or a hard-failure diagnostic) journaled for item
// index of foreach step group since that item's latest fresh entry, or nil
// if there is none: an earlier, crashed try's diagnostic is never reported
// for a re-run (mirrors journal.Replay's latest-entry-wins item reset).
func lastItemError(events []journal.Event, group string, index int) any {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind == journal.KindStepEnter && ev.Group == group && ev.Item != nil && *ev.Item == index && ev.HardRetry == 0 {
			return nil
		}
		if ev.Kind == journal.KindPostcondition && !ev.OK && ev.Group == group && ev.Item != nil && *ev.Item == index {
			return ev.Text
		}
	}
	return nil
}

// resumeForeach continues a foreach step after a crash: items that already
// transitioned (rs.ItemOutcome) are never re-run, every other item — one
// entered but unresolved, or never entered at all — runs inline (the same
// fix-forward caveat DESIGN.md §4 states for any interrupted step), then
// the join. If the crash came before the step's own STEP_ENTER carrying the
// list snapshot was journaled (an entry that resolved at entry — not an
// array, over max_items, empty — never journals one), there is nothing to
// resume from and the step is entered afresh. A blocked-run intervention
// never reaches here: Resume routes it to dispatchParallel, re-entering the
// whole step.
func (e *Engine) resumeForeach(dir string, log *journal.Log, runID string, step *spec.Step, rs *journal.RunState) (Instruction, error) {
	events, err := journal.ReadEvents(dir)
	if err != nil {
		return nil, err
	}
	hasSnapshot := false
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind == journal.KindStepEnter && ev.Step == step.ID && ev.Group == "" {
			hasSnapshot = len(ev.Items) > 0
			break
		}
	}
	if !hasSnapshot {
		return e.dispatchForeach(dir, log, runID, step, rs.Cursor)
	}
	return e.runForeachItems(dir, log, runID, step, rs.Cursor.Attempt, rs.ForeachItems[step.ID], rs.ItemOutcome[step.ID])
}
