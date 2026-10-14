package cli

import (
	"errors"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/dcferreira/agent-pawl/internal/engine"
	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// The frozen exit-code table for every pawl command except `hook`.
const (
	// exitOK: the command succeeded.
	exitOK = 0
	// exitNotFound: a run, workflow or root could not be found, or the
	// name was ambiguous.
	exitNotFound = 1
	// exitUsage: the caller's input is wrong — bad usage/flags, or a
	// workflow file that fails to parse or validate.
	exitUsage = 2
	// exitBlocked: the run is blocked (run/submit/poll only); paused and
	// resumable, not an error.
	exitBlocked = 3
	// exitRefused: the command declined to act (lock held, workflow
	// changed, wrong step, run already finished, enforcement gate).
	exitRefused = 4
	// exitInternal: an internal or environment error, including a
	// recovered panic.
	exitInternal = 5
)

// Hook-protocol exit codes, used only by `pawl hook`. Hook entry points
// fail open and never exit 2: Claude Code reads exit 2 as "block", so
// these are deliberately not the table above.
const (
	// hookExitOK: allow (silently), or deny/block via a JSON decision on
	// stdout.
	hookExitOK = 0
	// hookExitFailOpen: a non-blocking error (usage, or a pre-hook fail
	// open).
	hookExitFailOpen = 1
)

// commandHook is a test seam: when non-nil it is called with the command
// name at the point a command is dispatched (and again inside `hook`'s own
// recover scope), so tests can inject a panic.
var commandHook func(name string)

// RecoverPanic is meant to be deferred directly (`defer RecoverPanic(w,
// &code)`): on a panic it prints "pawl: internal error: <value>" and the
// stack to w and sets *code to exitInternal, so a Go panic is not
// mistaken for the runtime's default exit 2 ("your input is wrong").
func RecoverPanic(w io.Writer, code *int) {
	if r := recover(); r != nil {
		fmt.Fprintf(w, "pawl: internal error: %v\n%s", r, debug.Stack())
		*code = exitInternal
	}
}

// instructionExitCode maps the instruction pawl run/submit/poll is about to
// print to the one row of the frozen exit-code table (exitcode.go) that
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
		return exitBlocked
	}
	return exitOK
}

// exitForEngineErr classifies an error returned by Engine.Start, .Resume,
// .Submit, .SubmitHuman or .Poll into the refusal (exitRefused) vs
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
//     which are expected outcomes with a clear next step; or
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
// returned into the frozen exit-code table (exitcode.go): a YAML syntax error or an
// unknown field (spec.ErrParse) is "validation failed" — exit 2, the file
// resolved fine, its content is what's wrong — while anything else (the
// file doesn't exist, isn't readable) is a resolution error, exit 1, the
// same bucket an unknown workflow name falls into. Used by both pawl run
// and pawl validate, which both call loadAndValidate before ever
// constructing an Engine.
func exitForLoadErr(err error) int {
	if errors.Is(err, spec.ErrParse) {
		return exitUsage
	}
	return exitNotFound
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
		return exitInternal
	}
	return exitNotFound
}

func exitForEngineErr(err error) int {
	switch {
	case errors.Is(err, journal.ErrNewerFormat),
		errors.Is(err, journal.ErrHeld),
		errors.Is(err, engine.ErrDigestMismatch),
		errors.Is(err, engine.ErrAlreadyTerminal),
		errors.Is(err, engine.ErrRefused):
		return exitRefused
	default:
		return exitInternal
	}
}

// opaqueSentence is the cmd-less refusal text for a run this build cannot
// replay (journal.RunRef.Opaque): (a) for a run written by a newer pawl,
// (b) for a run of a known format whose events are unreadable. An unknown
// format stamp (0) renders as 1: an unstamped journal is format 1.
func opaqueSentence(ref *journal.RunRef) string {
	o := ref.Opaque
	if o.Ended {
		msg := fmt.Sprintf("run %s has already ended (%s)", ref.RunID, ref.StatusText())
		if o.EndReason != "" {
			msg += ": " + o.EndReason
		}
		return msg
	}
	if o.Newer() {
		return newerSentence(ref.RunID, o.Format)
	}
	return fmt.Sprintf("run %s cannot be replayed (format %d): %v; repair its events.jsonl, or `pawl abandon --run %s`", ref.RunID, journal.NormalizeFormat(o.Format), o.Err, ref.RunID)
}

func newerSentence(runID string, format int) string {
	return fmt.Sprintf("run %s was written by a newer pawl (format %d; this pawl reads formats 1-%d); upgrade, or `pawl abandon --run %s`", runID, format, journal.Format, runID)
}

// opaqueRefusal prints the refusal for an opaque run under cmd (e.g. "pawl
// submit") to stderr and returns its exit code: 4 for a newer format, 5 for
// an unreadable one.
func opaqueRefusal(stderr io.Writer, cmd string, ref *journal.RunRef) int {
	printLine(stderr, cmd+":", opaqueSentence(ref))
	if ref.Opaque.Newer() {
		return exitRefused
	}
	return exitInternal
}

// printEngineErr prints err (from an Engine call or loadPinnedWorkflow) under
// cmd and returns its exit code. A *journal.FormatError — a newer-format
// journal or plan met on an engine path — gets the same text and exit 4 as
// opaqueRefusal; anything else is "cmd: err" with fallback unless
// exitForEngineErr says it is a refusal.
func printEngineErr(stderr io.Writer, cmd, runID string, err error, fallback int) int {
	var fe *journal.FormatError
	if errors.As(err, &fe) {
		printLine(stderr, cmd+":", newerSentence(runID, fe.Format))
		return exitRefused
	}
	printLine(stderr, cmd+":", err.Error())
	if code := exitForEngineErr(err); code != exitInternal {
		return code
	}
	return fallback
}
