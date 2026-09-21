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
	ref, err := findLiveRun(root, runID)
	if err != nil {
		// A run that is no longer live is the same expected race
		// engine.ErrPollNotCurrent covers: another process finished or
		// abandoned it while this WAIT line was sitting in the session's
		// scrollback. Exit 0, having done nothing (DESIGN.md §3).
		printLine(stdout, "poll: nothing to do:", err.Error())
		return 0
	}

	// Same reasoning as pawl submit's: the run's own pinned copy of the
	// workflow, never a re-resolution by name off whatever is on disk now.
	pinned, err := loadPinnedWorkflow(ref.Dir)
	if err != nil {
		printLine(stderr, "pawl poll:", err.Error())
		return 1
	}
	if pinned.Changed {
		printLine(stderr, fmt.Sprintf("pawl poll: workflow file changed since run %s started (changed: %s); this run cannot continue safely — abandon it with `pawl abandon --run %s` and start a fresh run", runID, pinned.Detail, runID))
		return 1
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
		return 1
	}
	fmt.Fprint(stdout, formatInstruction(instr, w, root))
	return 0
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
