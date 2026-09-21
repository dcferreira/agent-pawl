// Package journal implements the run directory, the append-only event log,
// pure replay and the run lock (DESIGN.md §4). It is the only memory the
// engine has across process boundaries: `pawl run` starts a run and exits;
// `pawl submit` is a brand-new process that reconstructs the run's state from
// the events already on disk before it can act.
//
// journal depends on internal/spec (for the Workflow type stored in
// plan.json and hashed for the definition digest) and on nothing else
// internal.
package journal

import "time"

// Kind names one of the journal's event kinds (DESIGN.md §4).
type Kind string

// The event kinds in scope for this build.
const (
	KindRunStart      Kind = "RUN_START"
	KindResume        Kind = "RESUME"
	KindStepEnter     Kind = "STEP_ENTER"
	KindWrites        Kind = "WRITES"
	KindPostcondition Kind = "POSTCONDITION"
	KindTransition    Kind = "TRANSITION"
	// KindHumanAsked marks the moment a `human` step's question was asked
	// (DESIGN.md §4): its own Time is the deadline anchor `SubmitHuman`
	// measures `timeout:` against, since a `human` step has no background
	// poller the way `wait` does — the session is already blocked inside its
	// own AskUserQuestion call, so the deadline can only be enforced when the
	// answer is finally submitted.
	KindHumanAsked Kind = "HUMAN_ASKED"
	KindRunEnd     Kind = "RUN_END"
)

// Event is one line of events.jsonl. Every event carries RunID, Seq, Time,
// and (once the run is under way) Step and Attempt; the remaining fields are
// populated according to Kind, per DESIGN.md §4. Event is marshalled with
// encoding/json only — never a hand-rolled writer — so that JSON's escaping
// of control characters in map keys stays the last line of defence against a
// raw control byte breaking the one-record-per-line invariant.
type Event struct {
	Kind    Kind      `json:"kind"`
	RunID   string    `json:"run_id"`
	Seq     int       `json:"seq"`
	Time    time.Time `json:"time"`
	Step    string    `json:"step,omitempty"`
	Attempt int       `json:"attempt,omitempty"`

	// AttemptKey is the key the engine's postcondition-failure hasher
	// derived for this event's attempt budget (design/format-spec.md §B.4).
	// The engine stamps it on STEP_ENTER (authoritative: the key that
	// entry's attempt counts under) and on POSTCONDITION (provenance: the
	// key derived from that failure, which the next entry will use).
	// journal never computes it — only stores and keys Attempts by the
	// string it is given, so Replay stays pure and independent of the
	// workflow definition.
	AttemptKey string `json:"attempt_key,omitempty"`

	// Retry marks a STEP_ENTER that re-runs a step already entered for the
	// current visit (a postcondition-failure retry within the attempts:
	// budget), as opposed to a fresh arrival by an edge. Replay only counts
	// a non-Retry STEP_ENTER as a visit for max_visits:/max_steps: purposes
	// (design/format-spec.md §B.4): without this, every attempt-retry was
	// also miscounted as a fresh visit, which both over-counts max_visits:
	// across later visits and leaves it unenforced within one (a step could
	// retry past attempts: before max_visits: ever saw it happen).
	//
	// Additive and backward-compatible: an event recorded before this field
	// existed decodes with Retry false (its JSON zero value), so every
	// STEP_ENTER in an old journal still counts as a visit exactly as it
	// did before this field was added.
	Retry bool `json:"retry,omitempty"`

	// Group names the id of the parallel step that owns this event's Step,
	// when Step is one of that parallel step's branches: set on a
	// STEP_ENTER or TRANSITION for a BRANCH step, empty ("") for every
	// other event — including the parallel step's own (ungrouped)
	// STEP_ENTER/TRANSITION. Replay uses it to fold branch progress into
	// RunState.PendingBranches/BranchOutcome without disturbing the
	// singular lastEnter/transitionedSinceEnter/Cursor tracking, which
	// keeps following only the parallel step's own ungrouped events exactly
	// as before this field existed.
	//
	// Additive and backward-compatible: an event recorded before this field
	// existed decodes with Group "" (its JSON zero value), so it is never
	// mistaken for a branch event and Replay behaves exactly as it did
	// before this field was added.
	Group string `json:"group,omitempty"`

	// RUN_START
	Args         map[string]any `json:"args,omitempty"`
	Digest       string         `json:"digest,omitempty"`
	Resumed      bool           `json:"resumed,omitempty"`
	HookSelfTest string         `json:"hook_self_test,omitempty"`

	// RESUME: a crash resume, or a user intervention on a BLOCKED run
	// (Intervention true), which resets the step's attempt counter to 1
	// in this same record (the record's own Attempt field is that reset
	// value).
	Intervention bool `json:"intervention,omitempty"`

	// WRITES
	Writes map[string]any `json:"writes,omitempty"`

	// POSTCONDITION
	OK   bool   `json:"ok,omitempty"`
	Text string `json:"text,omitempty"`
	Soft bool   `json:"soft,omitempty"`

	// TRANSITION. ViaCatch marks a catch-chain edge, so Replay can apply
	// the attempt-budget clearing rule (design/format-spec.md §B.4): a
	// budget is cleared only when the step leaves by a non-catch edge.
	Target   string `json:"target,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	ViaCatch bool   `json:"via_catch,omitempty"`

	// RUN_END. Status "blocked" does not mean the run is over.
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
	Note   string `json:"note,omitempty"`
}
