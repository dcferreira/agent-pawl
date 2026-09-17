// Package journal implements the run directory, the append-only event log,
// pure replay and the run lock (DESIGN.md §4). It is the only memory the
// engine has across process boundaries: `wf run` starts a run and exits;
// `wf submit` is a brand-new process that reconstructs the run's state from
// the events already on disk before it can act.
//
// journal depends on internal/spec (for the Workflow type stored in
// plan.json and hashed for the definition digest) and on nothing else
// internal.
package journal

import "time"

// Kind names one of the journal's event kinds (DESIGN.md §4). HUMAN_ASKED is
// out of scope for this build (no `human` kind exists).
type Kind string

// The event kinds in scope for this build.
const (
	KindRunStart      Kind = "RUN_START"
	KindResume        Kind = "RESUME"
	KindStepEnter     Kind = "STEP_ENTER"
	KindWrites        Kind = "WRITES"
	KindPostcondition Kind = "POSTCONDITION"
	KindTransition    Kind = "TRANSITION"
	KindRunEnd        Kind = "RUN_END"
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
