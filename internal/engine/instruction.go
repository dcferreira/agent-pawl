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
// Dispatch, Ask or Terminal. Formatting the instruction for a human or a
// session belongs to internal/cli (Task 7), not here.
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

// Wait instructs the caller that the run has parked at a kind: wait step
// (DESIGN.md §2's WAIT line, §3's wait paragraph). The engine deliberately
// does *no* work here — in particular it never runs the step's poll:, not
// even once: the polling loop is pawl poll's job, which the session runs
// under Monitor because Claude's Bash tool ceiling is minutes and a CI wait
// is hours. pawl poll then submits on its own behalf; the model never runs
// pawl submit for a wait result (design/format-spec.md §13), which
// Engine.Submit enforces.
//
// Every and Timeout are the step's declared poll interval (defaulted to
// spec.DefaultEvery at load) and hard deadline, carried so the caller can
// print them alongside the poll command without re-reading the workflow.
type Wait struct {
	RunID string
	Step  string

	Every   string
	Timeout string
}

func (Wait) isInstruction() {}

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

// Ask instructs the caller to put a question to a person with
// AskUserQuestion (design/format-spec.md §B.5: "`human` steps map 1:1 onto
// AskUserQuestion") and then call SubmitHuman with what they answer. Like
// Dispatch, it carries everything already resolved — Options is the step's
// static list, or (for options_from:) the state key's list resolved at ask
// time, in order — so the caller never has to re-read run state itself.
type Ask struct {
	RunID   string
	Step    string
	Attempt int

	// Question is the step's question:, ${key}-substituted.
	Question string
	// Options is the resolved option list, in order: the step's static
	// options: as authored, or — for options_from: — the named state key's
	// list of strings as it stood at ask time (design/format-spec.md §B.5:
	// "resolved at ask time"). "Other" free text is always available in
	// addition and is never itself a member of Options.
	Options []string
	// Multi is the step's multi: (design/format-spec.md §D); true forces
	// the outcome to the reserved token "chosen" regardless of what was
	// picked.
	Multi bool
}

func (Ask) isInstruction() {}
