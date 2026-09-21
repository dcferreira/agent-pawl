package engine

import "errors"

// ErrDigestMismatch marks a Resume refusal because the workflow file has
// changed since the run started (DESIGN.md §4 step 2); the caller (Task 7)
// is expected to offer --fresh.
var ErrDigestMismatch = errors.New("engine: workflow file has changed since this run started")

// ErrAlreadyTerminal marks a Resume or Submit refusal because the run has
// already finished (a non-blocked RUN_END has been replayed).
var ErrAlreadyTerminal = errors.New("engine: run has already finished")

// errTimeout marks an execShell call that hit the engine-wide wall-clock
// ceiling; it is handled internally and never escapes this package.
var errTimeout = errors.New("engine: command exceeded the wall-clock ceiling")

// errContextUnavailable marks a context: entry the engine could not gather
// (a missing file, or a "!cmd" entry that failed to run) — an authoring bug
// discovered only at dispatch time, exactly like emit.ErrParse (finding I3).
// It is handled internally (routed as a failure) and never escapes this
// package: finding N2 was this same wedge class reintroduced at a new site
// by A1's fix, and it must be routed, not thrown, for the same reason.
var errContextUnavailable = errors.New("engine: context entry unavailable")

// errOptionsUnavailable marks a `human` step's options_from: state key that
// could not be resolved into a usable option list at ask time (missing,
// empty, or not a list of strings) — an authoring bug discovered only at
// dispatch time, handled the same way errContextUnavailable is on agentic
// (N2's precedent): routed via the reserved "failure" outcome, never thrown.
var errOptionsUnavailable = errors.New("engine: options_from: state key unavailable")
