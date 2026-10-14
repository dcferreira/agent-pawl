package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/dcferreira/agent-pawl/internal/engine"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// findAbandonFallback resolves runID among live runs in working copies other
// than the cwd's (cwdSlug), for a run whose working copy is deleted or
// elsewhere. It returns the single abandonable candidate, or nil and the exit
// code after printing the refusal to stderr.
func findAbandonFallback(runID, cwdSlug string, stderr io.Writer) (*journal.RunRef, int) {
	cands, err := journal.FindLiveRunElsewhere(runID, cwdSlug)
	if err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return nil, exitForLookupErr(err)
	}
	switch len(cands) {
	case 0:
		printLine(stderr, fmt.Sprintf("pawl abandon: no run %q for this working copy, and no live run %q in any other working copy", runID, runID))
		return nil, 1
	case 1:
	default:
		names := make([]string, len(cands))
		for i, c := range cands {
			names[i] = fmt.Sprintf("%s/%s/%s (%s)", c.Slug, c.WorkflowID, c.RunID, c.State.Status())
		}
		sort.Strings(names)
		printLine(stderr, fmt.Sprintf("pawl abandon: run id %q matches live runs in several other working copies; abandon nothing: %s", runID, strings.Join(names, ", ")))
		return nil, 1
	}
	c := cands[0]
	// A blocked run is paused, nobody is mid-step in it: always abandonable.
	if c.State.Status() != "blocked" && runBeingDriven(c.Dir) {
		printLine(stderr, fmt.Sprintf("pawl abandon: run %s in %s (workflow %s) is being driven by an active session; run pawl abandon from its working copy", runID, c.Slug, c.WorkflowID))
		return nil, exitForEngineErr(fmt.Errorf("%w: run %q is being driven", journal.ErrHeld, runID))
	}
	return &c, 0
}

// runBeingDriven reports whether dir's run looks actively driven: its lock is
// held by a live pid, or driver.json names a session whose heartbeat is
// fresh (journal.HeartbeatTTL). Unreadable evidence counts as "not driven".
func runBeingDriven(dir string) bool {
	if _, alive, err := journal.LockHolder(dir); err == nil && alive {
		return true
	}
	d, ok, err := journal.ReadDriver(dir)
	if err != nil || !ok {
		return false
	}
	hb, ok, err := journal.ReadSessionHeartbeat(d.SessionID)
	return err == nil && ok && hb.Fresh(time.Now())
}

// cmdAbandon implements pawl abandon --run <id> [--reason <text>]
// (design/format-spec.md §I): always available, always terminal. There is no
// Engine.Abandon — the engine has only Start/Resume/Submit — so this appends
// the RUN_END event directly via internal/journal, the same package the
// engine itself uses to end a run, with status "abandoned" (anything but
// "blocked" is terminal per journal.RunState.Terminal).
func cmdAbandon(args []string, cwd string, stdout, stderr io.Writer) int {
	var runID, reason string
	haveReason := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl abandon: --run needs a value")
				return 2
			}
			runID = args[i]
		case "--reason":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl abandon: --reason needs a value")
				return 2
			}
			reason = args[i]
			haveReason = true
		default:
			printLine(stderr, "pawl abandon: unrecognised argument", args[i])
			return 2
		}
	}
	if runID == "" {
		fmt.Fprintln(stderr, "usage: pawl abandon --run <id> [--reason <text>]")
		return 2
	}
	// --reason is optional free text; omitting it (or passing "") keeps the
	// journalled reason that existed before --reason was added, so an old
	// journal reading this code's output back is unaffected either way.
	if !haveReason || reason == "" {
		reason = "abandoned by user"
	}

	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}
	ref, err := findRunByID(root, runID)
	elsewhere := false
	if errors.Is(err, journal.ErrNoRun) {
		// Only a zero-match in this working copy falls back: any match
		// (live or terminal), ambiguity or I/O error above is final.
		var code int
		ref, code = findAbandonFallback(runID, journal.Slug(root), stderr)
		if ref == nil {
			return code
		}
		elsewhere = true
		err = nil
	}
	if err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return exitForLookupErr(err)
	}
	if ref.State.Terminal() {
		// Unlike pawl submit/poll, pawl abandon has no engine call to let
		// discover this on its own (it appends RUN_END directly) — a
		// terminal run is a refusal it must check for itself, exit 4, with
		// its own clear message rather than silently re-ending an already-
		// finished run with a second RUN_END event.
		printLine(stderr, fmt.Sprintf("pawl abandon: run %s has already ended (%s); nothing to abandon", runID, ref.State.Status()))
		return exitForEngineErr(fmt.Errorf("%w: run %q ended %s", engine.ErrAlreadyTerminal, runID, ref.State.EndStatus))
	}

	// findRunByID already confirmed runID exists and is live; anything failing from here
	// on is either the same lock-held refusal every other command can hit
	// (exit 4) or a broken run directory / journal it could not act on
	// (exit 5) — docs/cli.md's exit-code table (~line 33), which applies to
	// pawl abandon exactly as it does to run/submit/poll.
	lock, err := journal.AcquireLock(ref.Dir, false)
	if err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return exitForEngineErr(err)
	}
	defer lock.Release()

	log, err := journal.OpenLog(ref.Dir)
	if err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return exitForEngineErr(err)
	}
	defer log.Close()

	if elsewhere {
		printLine(stderr, fmt.Sprintf("pawl abandon: run %s is not in this working copy; abandoning it in %s (workflow %s)", runID, ref.Slug, ref.WorkflowID))
	}
	if _, err := log.Append(journal.Event{
		Kind:   journal.KindRunEnd,
		RunID:  runID,
		Step:   ref.State.Cursor.Step,
		Status: "abandoned",
		Reason: reason,
	}); err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return exitForEngineErr(err)
	}

	if elsewhere {
		// The post-command sync in Run only reconciles the cwd's slug.
		_ = journal.SyncLiveIndexSlug(ref.Slug)
	}
	fmt.Fprint(stdout, formatTerminal(engine.Terminal{
		RunID:   runID,
		Status:  "abandoned",
		StepID:  ref.State.Cursor.Step,
		Outcome: "abandoned",
		Message: fmt.Sprintf("run %s abandoned at step %q by user request.", runID, ref.State.Cursor.Step),
	}, ""))
	return 0
}
