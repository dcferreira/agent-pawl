package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/dcferreira/agent-pawl/internal/engine"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// cmdSubmit implements pawl submit --run <id> --step <id> --json '<result>'
// (DESIGN.md §2): an internal command the /pawl skill calls after a subagent
// returns, never written by an author. The attempt number is not a flag —
// it is read off the run's own journal, since the model has no reason to
// track it and the engine refuses any (run, step, attempt) triple but the
// one it is actually waiting on.
func cmdSubmit(args []string, cwd string, stdout, stderr io.Writer) int {
	var runID, stepID string
	var result string
	var haveResult bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl submit: --run needs a value")
				return 2
			}
			runID = args[i]
		case "--step":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl submit: --step needs a value")
				return 2
			}
			stepID = args[i]
		case "--json":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl submit: --json needs a value")
				return 2
			}
			result = args[i]
			haveResult = true
		default:
			printLine(stderr, "pawl submit: unrecognised argument", args[i])
			return 2
		}
	}
	if runID == "" || stepID == "" || !haveResult {
		fmt.Fprintln(stderr, "usage: pawl submit --run <id> --step <id> --json '<result>'")
		return 2
	}

	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}
	ref, err := findLiveRun(root, runID)
	if err != nil {
		printLine(stderr, "pawl submit:", err.Error())
		return 1
	}

	// C1/C2: never re-resolve the workflow by the run directory's own
	// workflow: id — that id need not be a resolvable filename at all (C1),
	// and re-resolving by name from cwd re-reads whatever is on disk right
	// now, which is a mid-run edit pawl submit must never silently adopt
	// (C2). plan.json's own recorded copy is what pawl run pinned for this
	// run (DESIGN.md §4: "immutable for the run"); loadPinnedWorkflow also
	// performs the same digest check Engine.Resume gives pawl run.
	pinned, err := loadPinnedWorkflow(ref.Dir)
	if err != nil {
		printLine(stderr, "pawl submit:", err.Error())
		return 1
	}
	if pinned.Changed {
		printLine(stderr, fmt.Sprintf("pawl submit: workflow file changed since run %s started (changed: %s); this run cannot continue safely — abandon it with `pawl abandon --run %s` and start a fresh run", runID, pinned.Detail, runID))
		return 1
	}
	w := pinned.Workflow

	e := engine.New(w, root)
	instr, err := e.Submit(runID, stepID, ref.State.Cursor.Attempt, json.RawMessage(result))
	if err != nil {
		printLine(stderr, "pawl submit:", err.Error())
		return 1
	}
	fmt.Fprint(stdout, formatInstruction(instr, w, root))
	return 0
}

// findLiveRun locates runID among root's non-terminal runs, across every
// workflow id, for commands (submit, abandon) that take only a run id.
func findLiveRun(root, runID string) (*journal.RunRef, error) {
	live, err := journal.Live(root)
	if err != nil {
		return nil, err
	}
	for i := range live {
		if live[i].RunID == runID {
			return &live[i], nil
		}
	}
	return nil, fmt.Errorf("no live run %q for this working copy", runID)
}
