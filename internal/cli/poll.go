package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/dcferreira/agent-pawl/internal/engine"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// cmdPoll implements pawl poll --run <id> --step <name> (DESIGN.md §2, §3):
// the command the model runs under Monitor when pawl run prints WAIT. It
// loops the step's poll: every every:, and when an iteration carries a routed
// token — or timeout: expires — it submits on its own behalf and prints the
// resulting DISPATCH/ASK/WAIT/TERMINAL line, exactly as pawl submit would.
// The model never runs pawl submit for a wait result
// (design/format-spec.md §13).
//
// Like pawl submit it takes no attempt number: the journal is authoritative
// about where the run is. Unlike pawl submit it takes no result at all — the
// poll: command's own stdout is the result.
func cmdPoll(args []string, cwd string, stdout, stderr io.Writer) int {
	var runID, stepID string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl poll: --run needs a value")
				return 2
			}
			runID = args[i]
		case "--step":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl poll: --step needs a value")
				return 2
			}
			stepID = args[i]
		default:
			printLine(stderr, "pawl poll: unrecognised argument", args[i])
			return 2
		}
	}
	if runID == "" || stepID == "" {
		fmt.Fprintln(stderr, "usage: pawl poll --run <id> --step <name>")
		return 2
	}

	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}
	ref, err := findRunByID(root, runID)
	if err != nil {
		// No run by this id at all (or an id ambiguous across workflows) is
		// a resolution error — the caller gave pawl poll something that was
		// never going to resolve, exit 1 — distinct from the two "nothing to
		// do" cases below, which both name a run that does/did exist.
		printLine(stderr, "pawl poll:", err.Error())
		return exitForLookupErr(err)
	}
	if ref.State.Terminal() {
		// The run existed and has already ended — the same expected race
		// engine.ErrPollNotCurrent covers below (another process finished or
		// abandoned it while this WAIT line sat in the session's scrollback):
		// pawl poll runs as an unattended background loop under Monitor
		// (DESIGN.md §3 treats "the run moved on" as expected, not an
		// error), so it exits 0 quietly rather than refusing, unlike pawl
		// submit/pawl abandon, which a person or the driving session reads
		// synchronously and can act on a refusal.
		printLine(stdout, fmt.Sprintf("poll: nothing to do: run %s has already ended (%s)", runID, ref.State.Status()))
		return 0
	}

	// Same reasoning as pawl submit's: the run's own pinned copy of the
	// workflow, never a re-resolution by name off whatever is on disk now.
	pinned, err := loadPinnedWorkflow(ref.Dir)
	if err != nil {
		// Same reasoning as pawl submit's: findLiveRun already confirmed
		// this run exists and is live, so a plan.json that has since become
		// unreadable is a broken run directory (exit 5), not an unknown run.
		printLine(stderr, "pawl poll:", err.Error())
		return 5
	}
	if pinned.Changed {
		printLine(stderr, fmt.Sprintf("pawl poll: workflow file changed since run %s started (changed: %s); this run cannot continue safely — abandon it with `pawl abandon --run %s` and start a fresh run", runID, pinned.Detail, runID))
		return 4
	}
	w := pinned.Workflow

	e := engine.New(w, root)
	instr, err := e.Poll(runID, stepID, func(it engine.PollIteration) {
		fmt.Fprint(stdout, formatPollIteration(it))
		flush(stdout)
	})
	if err != nil {
		if errors.Is(err, engine.ErrPollNotCurrent) {
			printLine(stdout, "poll: nothing to do:", err.Error())
			return 0
		}
		printLine(stderr, "pawl poll:", err.Error())
		return exitForEngineErr(err)
	}
	stampDriver(root, w.Workflow, runID)
	fmt.Fprint(stdout, formatInstruction(instr, w, root))
	return instructionExitCode(instr)
}

// flush pushes a per-iteration line out immediately when stdout is a
// flushable writer, so a session watching under Monitor sees the loop
// progressing rather than one burst at exit. os.Stdout is unbuffered, so this
// is a no-op there and matters only for a wrapped writer.
func flush(w io.Writer) {
	if f, ok := w.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
}
