// Package engine turns the four lower packages (spec, render, emit, journal)
// into a running workflow: it executes deterministic steps, evaluates
// postconditions, resolves outcomes against an explicit table, owns the
// attempt/visit counters, and drives the run loop between agentic dispatches
// (DESIGN.md §2, §3, §4). It never prints anything: every decision is
// returned as a value (Dispatch or Terminal) for a caller — internal/cli in
// this build — to format.
package engine

// ContextItem is one rendered context: entry, ready to print in a DISPATCH
// block.
type ContextItem struct {
	// Source is the entry as authored (a file path or a "!cmd ..." string),
	// after ${key} substitution.
	Source string
	// Value is the gathered content: the file's contents, or (when Source
	// names an existing file relative to the working-copy root) that file's
	// contents; engine does not execute arbitrary commands for context
	// entries in this build (see the engine package doc for why).
	Value string
}

// Instruction is what the engine hands back to its caller: exactly one of
// Dispatch or Terminal. Formatting the instruction for a human or a session
// belongs to internal/cli (Task 7), not here.
type Instruction interface {
	isInstruction()
}

// Dispatch instructs the caller to compose a subagent prompt from the
// rendered fields and dispatch it, then call Submit with what comes back
// (design/format-spec.md §B.6, DESIGN.md §2).
type Dispatch struct {
	RunID   string
	Step    string
	Attempt int

	// Description is the step's description:, ${key}-substituted.
	Description string
	// Context is gathered per the step's context: list, in declared order.
	Context []ContextItem
	// WritesKeys and WritesTypes together are the return schema: WritesKeys
	// is alphabetical (per internal/spec's normalisation), never
	// author-order.
	WritesKeys  []string
	WritesTypes map[string]string
	// SubagentArgs is passed through verbatim, uninterpreted.
	SubagentArgs any

	// PreviousFailure is the prior attempt's postcondition failure text,
	// set from attempt 2 onward; "" on the first attempt.
	PreviousFailure string
	// Interrupted is true when this attempt is being redispatched after a
	// crash or a blocked-run intervention: the caller should be told to
	// inspect current state before acting (design/format-spec.md §B.8).
	Interrupted bool
}

func (Dispatch) isInstruction() {}

// DispatchParallel instructs the caller to dispatch every entry in Agentic
// as a subagent — in parallel, as separate Agent tool calls in one message —
// and call Submit once per branch as each returns. Deterministic branches
// have already executed in-process before this is returned (same rule as
// the engine executing consecutive deterministic steps itself); their
// results are already journaled and are not repeated here.
type DispatchParallel struct {
	RunID, Step string // Step is the PARALLEL step's id
	Attempt     int
	Agentic     []Dispatch // each Dispatch.Step is a BRANCH's own id

	// Interrupted mirrors Dispatch.Interrupted: true when this
	// DispatchParallel is a crash-resume redispatch of the branches that
	// were still outstanding (never re-dispatching a branch that already
	// transitioned).
	Interrupted bool
}

func (DispatchParallel) isInstruction() {}

// BranchRecorded is returned by Submit for a branch report that leaves
// siblings still outstanding — not a new instruction for the session to
// act on (every agentic branch was already dispatched up front).
type BranchRecorded struct {
	RunID, ParallelStep, BranchStep string
	Remaining                       []string // other branch step ids still pending, sorted
}

func (BranchRecorded) isInstruction() {}

// Terminal instructs the caller that the run is over (Status "ok") or
// paused for review (Status "blocked" — DESIGN.md §4 is explicit that this
// is not a dead end).
type Terminal struct {
	RunID   string
	Status  string
	StepID  string
	Outcome string
	Message string
}

func (Terminal) isInstruction() {}
