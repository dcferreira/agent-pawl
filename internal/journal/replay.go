package journal

import (
	"fmt"
	"strings"
)

// AttemptRef identifies one attempt budget: a step id plus the attempt key
// its current failure streak is keyed on (design/format-spec.md §B.4). The
// same failure text (same key) continues a countdown across re-entries; a
// different failure starts a fresh budget. journal never computes the key —
// it only stores and indexes by the string an event carries.
type AttemptRef struct {
	Step string
	Key  string
}

// Cursor names the run's current position: the step the engine is waiting
// on (or about to enter next), the attempt number that step is on, and the
// attempt key that attempt counts under (AttemptKey is "" for a step not yet
// entered at this cursor, since its key has not been computed yet).
type Cursor struct {
	Step       string
	Attempt    int
	AttemptKey string
	// HardRetry carries through the last STEP_ENTER/RESUME's Event.HardRetry
	// for this step: how many hard-failure retries (retry:, §B.16) have
	// already been spent on the body of the current attempt. The engine
	// (advanceDeterministic) reads it on resume to continue a hard-retry
	// loop interrupted by a crash at the same retry count, never advancing
	// it — see Event.HardRetry.
	HardRetry int
}

// RunState is everything Replay reconstructs from a run's events.
type RunState struct {
	// Args holds the run's argument bindings, bound once at RUN_START and
	// read-only thereafter.
	Args map[string]any
	// State holds every state key a step has written, by its most recent
	// value.
	State map[string]any

	// Attempts is, per (step, attempt key), the attempt number of its most
	// recent STEP_ENTER or RESUME. An entry is deleted when its postcondition
	// passed (or the step declares none — deterministic/wait/human may
	// legitimately have no postcondition, per §B.7, which is vacuously a
	// pass) and the step then leaves by a non-catch edge: the §B.4 clearing
	// rule, implemented as the full two-part conjunction it states, not
	// collapsed to "any non-catch edge" — see the comment at its
	// implementation in Replay for why that collapse would (almost) be
	// unobservable anyway, and why it is still implemented in full.
	Attempts map[AttemptRef]int
	// Visits is, per step id, the number of non-Retry STEP_ENTER events it
	// has had — unscoped by attempt key, since max_visits caps total edge
	// entries to the step regardless of which failure keyed each attempt. A
	// STEP_ENTER with Retry set (an attempt-retry within the same visit,
	// re-running the step it is already on) does not count: max_visits:
	// caps how many times a step is *entered*, not how many times its body
	// runs (design/format-spec.md §B.4) — see Event.Retry.
	Visits map[string]int

	// LastError is the pseudo-key populated from the text of the most
	// recent POSTCONDITION: cleared on a pass, set on a failure.
	LastError string
	// AttemptLastError is the value LastError held at the start of the
	// current attempt — frozen at the most recent non-group STEP_ENTER/
	// RESUME whose HardRetry was 0 (the first try of an attempt, whether a
	// fresh visit or an attempts:-level retry), and left untouched by any
	// later STEP_ENTER with HardRetry > 0 (a retry: hard-retry re-running
	// that same attempt's body). A retry: hard-retry of an attempt must see
	// the same ${last_error} its attempt's first try saw, never the
	// diagnostic journaled by the previous hard-failed try of the same
	// attempt (design/format-spec.md §B.16) — see
	// engine.runDeterministicAttempt's lastErrorOverride.
	AttemptLastError string
	// BlockedReason is the pseudo-key populated from the reason on the
	// most recent RUN_END{status: blocked}.
	BlockedReason string

	// Cursor is where a resumed run continues, per DESIGN.md §4 step 4.
	Cursor Cursor

	// PendingBranches is, per in-flight kind: parallel step id, the set of
	// its branch step ids not yet transitioned (present with value true
	// while outstanding; deleted once the branch's TRANSITION is replayed).
	// Populated only from events carrying Event.Group — the parallel step's
	// own (ungrouped) STEP_ENTER/TRANSITION never touches it, and never
	// touches Cursor either (see Replay).
	PendingBranches map[string]map[string]bool
	// BranchOutcome is, per branch step id, the Outcome of its resolving
	// TRANSITION, once replayed. Like PendingBranches, populated only from
	// grouped (Event.Group != "") events.
	BranchOutcome map[string]string

	// Ended is true once a RUN_END event has been replayed.
	Ended bool
	// EndStatus, EndReason and EndNote are the most recent RUN_END's
	// fields. EndStatus "blocked" does not mean the run is over.
	EndStatus string
	EndReason string
	EndNote   string

	// Enforcement is RUN_START's hook_self_test: what pawl run decided
	// about the enforcement hooks when this run started ("on (PreToolUse
	// heartbeat)", "off (--no-enforcement)", "off (PAWL_ENFORCEMENT=off)",
	// or "unknown"). An opt-out is bound for the run's lifetime: see
	// EnforcementOff.
	Enforcement string
}

// EnforcementOff reports whether this run was started with enforcement
// explicitly opted out (--no-enforcement or PAWL_ENFORCEMENT=off). The hooks
// leave such a run alone entirely — no driver stamping, no guards, no Stop
// refusal — which is what the run's banner promised ("NOT enforced").
func (rs *RunState) EnforcementOff() bool {
	return strings.HasPrefix(rs.Enforcement, "off")
}

// Status reports "running" until the first RUN_END, and thereafter that
// RUN_END's own status (which may be "blocked" — not terminal).
func (rs *RunState) Status() string {
	if rs.Ended {
		return rs.EndStatus
	}
	return "running"
}

// Terminal reports whether the run is over: a RUN_END was replayed whose
// status was not "blocked".
func (rs *RunState) Terminal() bool {
	return rs.Ended && rs.EndStatus != "blocked"
}

// Replay reconstructs a RunState from events, purely: no I/O, no side
// effects, no clock reads, no dependence on anything but its argument, and
// no knowledge of the workflow definition (attempt keys arrive on the events
// themselves — see Event.AttemptKey). Handing it any prefix of a real run's
// events.jsonl must yield the state that was true when that prefix was the
// whole file (DESIGN.md §6's property test) — that purity, and that
// guarantee, are the point of this function.
//
// Replay fails loudly, rather than silently resuming into the wrong state,
// on two kinds of malformed input: an unknown event Kind, and a Seq that is
// not the exact successor of the previous event's Seq (a gap or a repeat —
// both are corruption or reordering, never legitimate).
//
// Cursor implements both DESIGN.md §4 step 4 rules:
//
//   - crash resume (the replayed events do not end in RUN_END{blocked}): the
//     last TRANSITION's target, at attempt 1 and no attempt key (it has not
//     been entered yet, so no key has been computed for it); or, if the step
//     of the last STEP_ENTER/RESUME never transitioned, that step at that
//     attempt and key.
//   - blocked resume (the events end in RUN_END{blocked}): the step that
//     produced the blocked outcome (RUN_END.Step, or the last-entered step
//     if that is empty), at its current attempt and key. The engine resets
//     that attempt to 1 by appending a RESUME{intervention: true} event and
//     replaying again — Replay does not mutate state on the caller's
//     behalf, since it is pure.
//
// A RESUME event only un-ends a run that was BLOCKED: one that (validly, or
// through corruption) follows a RUN_END whose status was not "blocked" is
// not honoured as un-ending it, so a stray or duplicated RESUME can never
// resurrect a finished run into a Live() listing.
func Replay(events []Event) (*RunState, error) {
	rs := &RunState{
		Args:            map[string]any{},
		State:           map[string]any{},
		Attempts:        map[AttemptRef]int{},
		Visits:          map[string]int{},
		PendingBranches: map[string]map[string]bool{},
		BranchOutcome:   map[string]string{},
	}

	var lastEnter Cursor
	haveEnter := false
	transitionedSinceEnter := false
	lastTarget := ""
	blockedStep := ""
	lastKeyByStep := map[string]string{}
	lastPostconditionOKByStep := map[string]bool{}

	haveSeq := false
	prevSeq := 0

	for i, e := range events {
		if haveSeq && e.Seq != prevSeq+1 {
			return nil, fmt.Errorf("journal: replay: event %d: seq %d does not follow seq %d (gap or repeat)", i, e.Seq, prevSeq)
		}
		prevSeq = e.Seq
		haveSeq = true

		switch e.Kind {
		case KindRunStart:
			rs.Enforcement = e.HookSelfTest
			for k, v := range e.Args {
				rs.Args[k] = v
			}
		case KindResume:
			lastEnter = Cursor{Step: e.Step, Attempt: e.Attempt, AttemptKey: e.AttemptKey, HardRetry: e.HardRetry}
			haveEnter = true
			transitionedSinceEnter = false
			rs.Attempts[AttemptRef{e.Step, e.AttemptKey}] = e.Attempt
			lastKeyByStep[e.Step] = e.AttemptKey
			if e.HardRetry == 0 {
				rs.AttemptLastError = rs.LastError
			}
			// No POSTCONDITION has run yet for this entry: deterministic/
			// wait/human steps may legitimately have none at all (§B.7), in
			// which case there is nothing to fail and the step is free to
			// leave by a non-catch edge as soon as it wants to.
			lastPostconditionOKByStep[e.Step] = true
			// Only a resume out of BLOCKED un-ends the run: a RESUME that
			// (validly, mid-run-before-any-RUN_END, or invalidly, stray or
			// duplicated) follows a truly terminal RUN_END must not
			// resurrect it into Live() (I9).
			if rs.Ended && rs.EndStatus == "blocked" {
				rs.Ended = false
				rs.EndStatus = ""
			}
		case KindStepEnter:
			if e.Group != "" {
				// A grouped STEP_ENTER is a parallel step's branch entering:
				// it must never touch lastEnter/haveEnter/transitionedSince
				// Enter/rs.Cursor, which stay parked on the parallel step's
				// own ungrouped events.
				if rs.PendingBranches[e.Group] == nil {
					rs.PendingBranches[e.Group] = map[string]bool{}
				}
				rs.PendingBranches[e.Group][e.Step] = true
				break
			}
			lastEnter = Cursor{Step: e.Step, Attempt: e.Attempt, AttemptKey: e.AttemptKey, HardRetry: e.HardRetry}
			haveEnter = true
			transitionedSinceEnter = false
			rs.Attempts[AttemptRef{e.Step, e.AttemptKey}] = e.Attempt
			lastKeyByStep[e.Step] = e.AttemptKey
			lastPostconditionOKByStep[e.Step] = true
			if e.HardRetry == 0 {
				rs.AttemptLastError = rs.LastError
			}
			// A hard-failure retry (Event.HardRetry > 0, §B.16) re-runs the
			// same attempt's body, exactly like an attempts:-driven Retry
			// re-entry — neither counts as a fresh visit.
			if !e.Retry && e.HardRetry == 0 {
				rs.Visits[e.Step]++
			}
		case KindWrites:
			for k, v := range e.Writes {
				rs.State[k] = v
			}
		case KindPostcondition:
			lastPostconditionOKByStep[e.Step] = e.OK
			if e.OK {
				rs.LastError = ""
			} else {
				rs.LastError = e.Text
			}
		case KindTransition:
			if e.Group != "" {
				// A grouped TRANSITION resolves one branch: record its
				// outcome and clear it from the pending set, without
				// touching transitionedSinceEnter/lastTarget/rs.Cursor,
				// which track only the parallel step's own ungrouped
				// TRANSITION.
				if rs.PendingBranches[e.Group] != nil {
					delete(rs.PendingBranches[e.Group], e.Step)
				}
				rs.BranchOutcome[e.Step] = e.Outcome
				break
			}
			transitionedSinceEnter = true
			lastTarget = e.Target
			// The §B.4 clearing rule as stated is a conjunction: cleared
			// when the postcondition passed AND the step leaves by a
			// non-catch edge. Per §B.7, a failing postcondition — soft or
			// hard — "retries and routes exactly as a hard one does", so a
			// step whose postcondition failed either re-enters (no
			// TRANSITION at all) or exhausts and leaves via catch
			// (ViaCatch: true); a non-catch TRANSITION should therefore
			// never coincide with a failed postcondition in a
			// correctly-behaving engine, making !e.ViaCatch alone
			// equivalent in practice. The conjunction is still implemented
			// in full here rather than relying on that engine invariant:
			// Replay is meant to be safe against a malformed or
			// buggy-engine journal (it already checks Seq and event Kind
			// for the same reason), and the check costs nothing.
			if !e.ViaCatch && lastPostconditionOKByStep[e.Step] {
				delete(rs.Attempts, AttemptRef{e.Step, lastKeyByStep[e.Step]})
			}
		case KindHumanAsked:
			// No RunState field to update: the deadline check lives in
			// engine.SubmitHuman, which re-reads this exact event's own Time
			// off the journal directly (Replay carries no per-event
			// timestamps forward). This case exists purely so an unknown
			// Kind never trips Replay's own defensive "corrupt or malformed"
			// error below — a HUMAN_ASKED event is neither.
		case KindRunEnd:
			rs.Ended = true
			rs.EndStatus = e.Status
			rs.EndReason = e.Reason
			rs.EndNote = e.Note
			if e.Status == "blocked" {
				rs.BlockedReason = e.Reason
				blockedStep = e.Step
				if blockedStep == "" {
					blockedStep = lastEnter.Step
				}
			}
		default:
			return nil, fmt.Errorf("journal: replay: event %d: unknown kind %q", i, e.Kind)
		}
	}

	switch {
	case rs.Ended && rs.EndStatus == "blocked":
		step := blockedStep
		if step == "" {
			step = lastEnter.Step
		}
		key := lastKeyByStep[step]
		rs.Cursor = Cursor{Step: step, Attempt: rs.Attempts[AttemptRef{step, key}], AttemptKey: key}
	case transitionedSinceEnter:
		rs.Cursor = Cursor{Step: lastTarget, Attempt: 1}
	case haveEnter:
		rs.Cursor = lastEnter
	default:
		rs.Cursor = Cursor{}
	}

	return rs, nil
}
