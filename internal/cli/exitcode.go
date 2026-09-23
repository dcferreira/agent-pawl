package cli

import (
	"errors"

	"github.com/dcferreira/agent-pawl/internal/engine"
	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// instructionExitCode maps the instruction pawl run/submit/poll is about to
// print to the one row of docs/cli.md's exit-code table (~line 33) that
// depends on the workflow's own outcome rather than on whether the command
// itself succeeded: a run that leaves the cursor at a TERMINAL whose status
// is "blocked" exits 3 (paused, resumable, not an error — DESIGN.md §4).
// Every other instruction this package ever prints — DISPATCH/ASK/WAIT, or
// a TERMINAL with any other status — stays 0. This is deliberately the only
// place that decides exit 3: run.go, submit.go and poll.go all funnel their
// successful print through this one call, so "did we just print a blocked
// terminal" is answered once, on the engine.Instruction value itself, not
// re-derived from the printed text three different ways.
func instructionExitCode(instr engine.Instruction) int {
	if t, ok := instr.(engine.Terminal); ok && t.Status == "blocked" {
		return 3
	}
	return 0
}

// exitForEngineErr classifies an error returned by Engine.Start, .Resume,
// .Submit, .SubmitHuman or .Poll into docs/cli.md's refusal (4) vs
// engine-error (5) exit-code class — the two codes that only make sense
// once a command is past every check it performs itself before ever
// constructing an Engine (the workflow resolved, spec.Validate clean, the
// run found by journal.Live/FindRun). Everything reaching this point is
// either:
//
//   - a refusal: the run declining to act because the caller's request
//     does not match what it is actually waiting on right now — a lock
//     held by another live process (journal.ErrHeld), a workflow file that
//     changed since the run started (engine.ErrDigestMismatch), a submit
//     for the wrong step/kind or against a run that has already finished
//     (engine.ErrRefused, engine.ErrAlreadyTerminal) — none of which the
//     caller could have avoided by typing a different command, but all of
//     which are expected outcomes with a clear next step (docs/cli.md's
//     "Refusals you may see"); or
//   - a genuine engine error: a corrupt or unreadable run directory (a
//     journal.OpenLog/ReadPlan/Replay failure that got this far — the
//     resilient callers, journal.Live and journal.FindRun, already skip a
//     corrupt run directory rather than erroring, so an error surviving to
//     here means the run directory looked fine a moment ago and stopped
//     being readable under the engine's own hand), or an
//     "internal error: …" invariant spec.Validate should already have
//     ruled out. Nothing here is fixable by retyping the command, which is
//     exactly the distinction exit 5 exists to draw from exit 4.
//
// exitForLoadErr classifies an error spec.Load (via loadAndValidate)
// returned into docs/cli.md's exit-code table: a YAML syntax error or an
// unknown field (spec.ErrParse) is "validation failed" — exit 2, the file
// resolved fine, its content is what's wrong — while anything else (the
// file doesn't exist, isn't readable) is a resolution error, exit 1, the
// same bucket an unknown workflow name falls into. Used by both pawl run
// and pawl validate, which both call loadAndValidate before ever
// constructing an Engine.
func exitForLoadErr(err error) int {
	if errors.Is(err, spec.ErrParse) {
		return 2
	}
	return 1
}

// exitForLookupErr classifies an error journal.Live or journal.FindRun
// returned (a run/id resolution failure at the cli boundary, before ever
// touching an Engine) into resolution (1) vs engine error (5): journal.ErrIO
// marks a genuine I/O failure reading the state directory tree itself — a
// broken run directory, exit 5 — while everything else (no run by that id,
// an ambiguous id across workflows) is a plain "couldn't resolve it",
// exit 1, the same bucket an unknown workflow name falls into.
func exitForLookupErr(err error) int {
	if errors.Is(err, journal.ErrIO) {
		return 5
	}
	return 1
}

func exitForEngineErr(err error) int {
	switch {
	case errors.Is(err, journal.ErrHeld),
		errors.Is(err, engine.ErrDigestMismatch),
		errors.Is(err, engine.ErrAlreadyTerminal),
		errors.Is(err, engine.ErrRefused):
		return 4
	default:
		return 5
	}
}
