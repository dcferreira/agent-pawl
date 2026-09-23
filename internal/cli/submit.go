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
// returns, or after a person answers an ASK block's question, never written
// by an author. Same CLI surface either way — pawl submit does not gain a
// new subcommand for a human answer; it dispatches on the target step's
// kind, once resolved from the pinned workflow, to Engine.SubmitHuman
// (design/format-spec.md §I only documents one pawl submit command). The
// attempt number is not a flag — it is read off the run's own journal,
// since the model has no reason to track it and the engine refuses any
// (run, step, attempt) triple but the one it is actually waiting on.
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
	ref, err := findRunByID(root, runID)
	if err != nil {
		printLine(stderr, "pawl submit:", err.Error())
		return exitForLookupErr(err)
	}
	// findRunByID finds a run whether it is live or already terminal
	// (journal.FindRun), deliberately: submitting against a run that has
	// already finished is not "no such run" (exit 1) but a refusal (exit
	// 4) — engine.Submit/SubmitHuman's own rs.Terminal() check already
	// returns engine.ErrAlreadyTerminal for exactly this once the run is
	// replayed below, which cli's exitForEngineErr maps to 4. Letting a
	// terminal ref through to loadPinnedWorkflow/engine.Submit rather than
	// refusing it here reuses that one message instead of a second one.

	// C1/C2: never re-resolve the workflow by the run directory's own
	// workflow: id — that id need not be a resolvable filename at all (C1),
	// and re-resolving by name from cwd re-reads whatever is on disk right
	// now, which is a mid-run edit pawl submit must never silently adopt
	// (C2). plan.json's own recorded copy is what pawl run pinned for this
	// run (DESIGN.md §4: "immutable for the run"); loadPinnedWorkflow also
	// performs the same digest check Engine.Resume gives pawl run.
	pinned, err := loadPinnedWorkflow(ref.Dir)
	if err != nil {
		// findLiveRun already confirmed this run exists and is live; a
		// plan.json that has since become unreadable or unparsable is not
		// "unknown run" (exit 1) but a broken run directory (exit 5) —
		// docs/cli.md's exit-code table (~line 33).
		printLine(stderr, "pawl submit:", err.Error())
		return 5
	}
	if pinned.Changed {
		printLine(stderr, fmt.Sprintf("pawl submit: workflow file changed since run %s started (changed: %s); this run cannot continue safely — abandon it with `pawl abandon --run %s` and start a fresh run", runID, pinned.Detail, runID))
		// Same refusal engine.ErrDigestMismatch names for pawl run — exit 4.
		return 4
	}
	w := pinned.Workflow
	step := w.StepByID(stepID)

	e := engine.New(w, root)
	var instr engine.Instruction
	if step != nil && step.Kind == "human" {
		instr, err = e.SubmitHuman(runID, stepID, ref.State.Cursor.Attempt, json.RawMessage(result))
	} else {
		instr, err = e.Submit(runID, stepID, ref.State.Cursor.Attempt, json.RawMessage(result))
	}
	if err != nil {
		printLine(stderr, "pawl submit:", err.Error())
		return exitForEngineErr(err)
	}
	fmt.Fprint(stdout, formatInstruction(instr, w, root))
	return instructionExitCode(instr)
}

// findRunByID locates runID among every run directory for this working
// copy — live or already terminal — across every workflow id, for commands
// (submit, poll, abandon) that take only a run id. It is journal.FindRun
// under a name matching this package's other lookups; unlike an earlier
// version of this function (then named findLiveRun), it does not filter out
// a terminal run itself — each caller decides what a terminal ref means for
// it (pawl submit/poll let the engine or a quiet no-op handle it; pawl
// abandon refuses it explicitly), rather than this function silently
// reporting "no run" for a run that in fact exists but has ended.
func findRunByID(root, runID string) (*journal.RunRef, error) {
	return journal.FindRun(root, runID)
}
