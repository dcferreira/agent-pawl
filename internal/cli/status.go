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
			// "no run" / "ambiguous across workflows" is resolution (1);
			// journal.ErrIO (a broken state directory) is an engine error (5).
			return exitForLookupErr(err)
		}
	} else {
		live, err := journal.Live(root)
		if err != nil {
			printLine(stderr, err.Error())
			return exitForLookupErr(err)
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
	// ref was just found by FindRun/Live, so a plan.json that has since
	// become unreadable, or a pinned workflow spec.Validate itself can't
	// process, is a broken run directory (exit 5), not an unknown run.
	pinned, err := loadPinnedWorkflow(ref.Dir)
	if err != nil {
		printLine(stderr, err.Error())
		return 5
	}
	report, err := spec.Validate(pinned.Workflow)
	if err != nil {
		printLine(stderr, err.Error())
		return 5
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
	// pawl status is a reporting command, deliberately exempt from the rest
	// of the exit-code table's exit-3 row (run/submit/poll's BLOCKED
	// terminal): the state it reports is in status, reason and (--json)
	// the whole payload, not something a non-zero exit would add to. --run
	// on an ended run reaches this same line, and an abandoned or otherwise
	// terminal run is not an error to have looked up either. --json matches
	// this on purpose rather than inventing a status-only exit code.
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
