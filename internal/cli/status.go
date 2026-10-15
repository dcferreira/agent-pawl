package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
var statusCmd = &command{
	name:  "status",
	usage: cmdUsage(usageStatus),
	flags: []flagDef{{name: "run"}, {name: "json", isBool: true}},
}

func cmdStatus(args []string, cwd string, stdout, stderr io.Writer) int {
	pf, code, done := statusCmd.parseOrExit(args, stdout, stderr)
	if done {
		return code
	}
	runID, asJSON := pf.value("run"), pf.boolean("json")

	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		printLine(stderr, err.Error())
		return exitNotFound
	}

	var refs []journal.RunRef
	if runID != "" {
		ref, err := journal.FindRun(root, runID)
		if err != nil {
			printLine(stderr, "pawl status:", err.Error())
			// "no run" / "ambiguous across workflows" is resolution (1);
			// journal.ErrIO (a broken state directory) is an engine error (5).
			return exitForLookupErr(err)
		}
		refs = []journal.RunRef{*ref}
	} else {
		live, err := journal.Live(root)
		if err != nil {
			printLine(stderr, err.Error())
			return exitForLookupErr(err)
		}
		sort.Slice(live, func(i, j int) bool { return live[i].RunID < live[j].RunID })
		switch {
		case len(live) == 0:
			if asJSON {
				fmt.Fprint(stdout, formatStatusJSONEmpty(root))
			} else {
				w := &blockWriter{}
				w.line(0, "root:", root)
				w.literal("no live runs for this working copy\n")
				fmt.Fprint(stdout, w.String())
			}
			return exitOK
		case len(live) > 1 && !asJSON:
			ids := make([]string, len(live))
			for i, r := range live {
				ids[i] = r.RunID
				if r.Opaque != nil {
					ids[i] += " (" + r.StatusText() + ")"
				}
			}
			printLine(stderr, fmt.Sprintf("pawl status: multiple runs are live for this working copy; disambiguate with --run <id>: %s", strings.Join(ids, ", ")))
			return exitNotFound
		}
		// --json with several live runs reports all of them, in run-id
		// order (the order the text-mode refusal lists them in).
		refs = live
	}

	var jsonRuns []statusRunJSON
	for i := range refs {
		ref := &refs[i]
		if ref.Opaque != nil {
			// A run this build cannot replay: read-only, show the run and
			// why it cannot be driven. With --json it is a normal runs[] entry.
			switch {
			case asJSON:
				jsonRuns = append(jsonRuns, opaqueStatusRunJSON(ref, cwd))
			default:
				writeStatusDetailBlock(stdout, root, ref, ref.StatusText(), opaqueSentence(ref))
			}
			continue
		}
		// C1: read the workflow from plan.json, never by re-resolving the run
		// directory's own workflow: id as a filename (see loadPinnedWorkflow).
		// A plan.json that is unreadable, or a pinned workflow spec.Validate
		// can't process, makes the run unreadable here: it is listed (not
		// skipped) so one broken run never fails the whole listing.
		pinned, err := loadPinnedWorkflow(ref.Dir)
		var report *spec.Report
		if err == nil {
			report, err = spec.Validate(pinned.Workflow)
		}
		if err != nil {
			switch {
			case asJSON:
				jsonRuns = append(jsonRuns, unreadableStatusRunJSON(ref, cwd, err.Error(), "unreadable"))
			default:
				writeStatusDetailBlock(stdout, root, ref, "unreadable", err.Error())
			}
			continue
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
			jsonRuns = append(jsonRuns, newStatusRunJSON(
				resumeName(pinned.Workflow.Path, cwd), pinned.Workflow.Path, warning,
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
	}
	if asJSON {
		fmt.Fprint(stdout, formatStatusJSON(root, jsonRuns))
	}
	// pawl status is a reporting command, deliberately exempt from the rest
	// of the exit-code table's exit-3 row (run/submit/poll's BLOCKED
	// terminal): the state it reports is in status, reason and (--json)
	// the whole payload, not something a non-zero exit would add to. --run
	// on an ended run reaches this same line, and an abandoned or otherwise
	// terminal run is not an error to have looked up either. --json matches
	// this on purpose rather than inventing a status-only exit code.
	return exitOK
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

// writeStatusDetailBlock is the text-mode block for a run pawl status
// cannot show in full: opaque (newer-format/unreadable journal) or a run
// that failed to load.
func writeStatusDetailBlock(out io.Writer, root string, ref *journal.RunRef, status, detail string) {
	w := &blockWriter{}
	w.line(0, "root:", root)
	w.line(0, "run:", ref.RunID)
	w.line(0, "workflow:", ref.WorkflowID)
	w.line(0, "status:", status)
	w.line(0, "detail:", detail)
	fmt.Fprint(out, w.String())
}

// opaqueStatusRunJSON is the runs[] entry for a run this build cannot replay.
func opaqueStatusRunJSON(ref *journal.RunRef, cwd string) statusRunJSON {
	return unreadableStatusRunJSON(ref, cwd, opaqueSentence(ref), ref.StatusText())
}

// unreadableStatusRunJSON is the runs[] entry for a run whose state cannot be
// shown: the documented envelope, with empty values for what cannot be known.
// workflow_path is the path recorded in the run's own plan.json (decoded
// leniently, so a newer-format or otherwise damaged plan still yields it when
// it can), or "" when absent or unreadable; workflow is resumeName of that
// path (the name pawl run accepts) or "". The workflow ID is never
// re-resolved as a filename.
func unreadableStatusRunJSON(ref *journal.RunRef, cwd, reason, status string) statusRunJSON {
	workflowPath := ""
	if data, err := os.ReadFile(filepath.Join(ref.Dir, "plan.json")); err == nil {
		var plan struct {
			Workflow *struct {
				Path string `json:"path"`
			} `json:"workflow"`
		}
		if json.Unmarshal(data, &plan) == nil && plan.Workflow != nil {
			workflowPath = plan.Workflow.Path
		}
	}
	return statusRunJSON{
		RunID:        ref.RunID,
		Workflow:     resumeName(workflowPath, cwd),
		WorkflowPath: workflowPath,
		Status:       status,
		Visits:       map[string]int{},
		StateKeys:    []string{},
		Reason:       reason,
		Soft:         statusSoftJSON{StepIDs: []string{}},
	}
}
