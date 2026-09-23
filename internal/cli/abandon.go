package cli

import (
	"fmt"
	"io"

	"github.com/dcferreira/agent-pawl/internal/engine"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// cmdAbandon implements pawl abandon --run <id> (design/format-spec.md §I):
// always available, always terminal. There is no Engine.Abandon — the
// engine has only Start/Resume/Submit — so this appends the RUN_END event
// directly via internal/journal, the same package the engine itself uses to
// end a run, with status "abandoned" (anything but "blocked" is terminal
// per journal.RunState.Terminal).
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
	ref, err := findLiveRun(root, runID)
	if err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return 1
	}

	lock, err := journal.AcquireLock(ref.Dir, false)
	if err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return 1
	}
	defer lock.Release()

	log, err := journal.OpenLog(ref.Dir)
	if err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return 1
	}
	defer log.Close()

	if _, err := log.Append(journal.Event{
		Kind:   journal.KindRunEnd,
		RunID:  runID,
		Step:   ref.State.Cursor.Step,
		Status: "abandoned",
		Reason: reason,
	}); err != nil {
		printLine(stderr, "pawl abandon:", err.Error())
		return 1
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
