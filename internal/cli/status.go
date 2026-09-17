package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dcferreira/agentic-workflow-fsm/internal/journal"
	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// cmdStatus implements wf status [--run <id>] (design/format-spec.md §I):
// the resolved root, run id, current step, attempt, visits, restored state
// keys, and the soft: census.
func cmdStatus(args []string, cwd string, stdout, stderr io.Writer) int {
	var runID string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "wf status: --run needs a value")
				return 2
			}
			runID = args[i]
		default:
			printLine(stderr, "wf status: unrecognised argument", args[i])
			return 2
		}
	}

	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}
	live, err := journal.Live(root)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}

	var ref *journal.RunRef
	switch {
	case runID != "":
		for i := range live {
			if live[i].RunID == runID {
				ref = &live[i]
			}
		}
		if ref == nil {
			printLine(stderr, "wf status: no live run", runID, "for this working copy")
			return 1
		}
	case len(live) == 0:
		w := &blockWriter{}
		w.line(0, "root:", root)
		w.literal("no live runs for this working copy\n")
		fmt.Fprint(stdout, w.String())
		return 0
	case len(live) == 1:
		ref = &live[0]
	default:
		ids := make([]string, len(live))
		for i, r := range live {
			ids[i] = r.RunID
		}
		sort.Strings(ids)
		printLine(stderr, fmt.Sprintf("wf status: multiple runs are live for this working copy; disambiguate with --run <id>: %s", strings.Join(ids, ", ")))
		return 1
	}

	// C1: read the workflow from plan.json, never by re-resolving the run
	// directory's own workflow: id as a filename (see loadPinnedWorkflow).
	pinned, err := loadPinnedWorkflow(ref.Dir)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}
	report, err := spec.Validate(pinned.Workflow)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}

	warning := ""
	if pinned.Changed {
		warning = fmt.Sprintf("workflow file has changed since this run started (changed: %s); wf submit will refuse until you abandon and start fresh", pinned.Detail)
	}
	fmt.Fprint(stdout, formatStatus(
		root, pinned.Workflow.Path, warning,
		ref.RunID, ref.State.Status(), ref.State.Cursor.Step, ref.State.Cursor.Attempt,
		formatVisits(ref.State.Visits), formatKeySet(ref.State.State),
		report,
	))
	return 0
}

func formatVisits(visits map[string]int) string {
	if len(visits) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(visits))
	for k := range visits {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s=%d", n, visits[n])
	}
	return strings.Join(parts, ", ")
}
