package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// cmdStatus implements pawl status [--run <id>] [--json] (design/format-spec.md
// §I): the resolved root, run id, current step, attempt, visits, restored
// state keys, and the soft: census. --run looks a run up whether it is live
// or already terminal (journal.FindRun) — e.g. to show the reason an
// abandoned run was ended — but the no-flag "every live run" path still
// enumerates only journal.Live, matching `pawl run`'s own resume rule.
func cmdStatus(args []string, cwd string, stdout, stderr io.Writer) int {
	var runID string
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl status: --run needs a value")
				return 2
			}
			runID = args[i]
		case "--json":
			asJSON = true
		default:
			printLine(stderr, "pawl status: unrecognised argument", args[i])
			return 2
		}
	}

	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}

	var ref *journal.RunRef
	if runID != "" {
		ref, err = journal.FindRun(root, runID)
		if err != nil {
			printLine(stderr, "pawl status:", err.Error())
			return 1
		}
	} else {
		live, err := journal.Live(root)
		if err != nil {
			printLine(stderr, err.Error())
			return 1
		}
		switch len(live) {
		case 0:
			if asJSON {
				fmt.Fprint(stdout, formatStatusJSONEmpty(root))
			} else {
				w := &blockWriter{}
				w.line(0, "root:", root)
				w.literal("no live runs for this working copy\n")
				fmt.Fprint(stdout, w.String())
			}
			return 0
		case 1:
			ref = &live[0]
		default:
			ids := make([]string, len(live))
			for i, r := range live {
				ids[i] = r.RunID
			}
			sort.Strings(ids)
			printLine(stderr, fmt.Sprintf("pawl status: multiple runs are live for this working copy; disambiguate with --run <id>: %s", strings.Join(ids, ", ")))
			return 1
		}
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
	// pinned.Changed only matters for a still-live run: "pawl submit will
	// refuse" is meaningless once the run has ended (nothing left to
	// submit), and "abandon and start fresh" is meaningless on a run that
	// is itself the reason FindRun, not Live, found this ref (task B1) —
	// e.g. an already-abandoned run. Reviewer finding B6.
	if pinned.Changed && !ref.State.Terminal() {
		warning = fmt.Sprintf("workflow file has changed since this run started (changed: %s); pawl submit will refuse until you abandon and start fresh", pinned.Detail)
	}
	status := ref.State.Status()
	reason := ""
	if ref.State.Ended {
		reason = ref.State.EndReason
	}
	if asJSON {
		fmt.Fprint(stdout, formatStatusJSON(
			root, pinned.Workflow.Path, warning,
			ref.RunID, status, ref.State.Cursor.Step, ref.State.Cursor.Attempt,
			ref.State.Visits, ref.State.State, reason,
			report,
		))
	} else {
		fmt.Fprint(stdout, formatStatus(
			root, pinned.Workflow.Path, warning,
			ref.RunID, status, ref.State.Cursor.Step, ref.State.Cursor.Attempt,
			formatVisits(ref.State.Visits), formatKeySet(ref.State.State), reason,
			report,
		))
	}
	// docs/cli.md claims exit 3 when the selected run is BLOCKED, but this
	// build's cmdStatus has never implemented that (verified: no exit-3
	// path existed before --json either) — always 0 on a successful
	// lookup, blocked or not. --json intentionally matches that, rather
	// than inventing a new exit code docs/cli.md's own audit (task B3)
	// should instead correct.
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
