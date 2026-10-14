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
	exit := exitOK
	for i := range refs {
		ref := &refs[i]
		if ref.Opaque != nil {
			// A run this build cannot replay: read-only, show the run and
			// why it cannot be driven, exit 0. Only the single-run path
			// reports one; wiring opaque runs into the multi-run --json
			// output is a separate follow-up, so there one is skipped
			// with a stderr line (and exit 5) rather than crashing.
			switch {
			case len(refs) > 1:
				printLine(stderr, "pawl status:", ref.RunID+":", opaqueSentence(ref))
				exit = exitInternal
			case asJSON:
				jsonRuns = append(jsonRuns, opaqueStatusRunJSON(ref))
			default:
				w := &blockWriter{}
				w.line(0, "root:", root)
				w.line(0, "run:", ref.RunID)
				w.line(0, "workflow:", ref.WorkflowID)
				w.line(0, "status:", ref.StatusText())
				w.line(0, "detail:", opaqueSentence(ref))
				fmt.Fprint(stdout, w.String())
			}
			continue
		}
		// C1: read the workflow from plan.json, never by re-resolving the run
		// directory's own workflow: id as a filename (see loadPinnedWorkflow).
		// ref was just found by FindRun/Live, so a plan.json that has since
		// become unreadable, or a pinned workflow spec.Validate itself can't
		// process, is a broken run directory (exit 5), not an unknown run.
		pinned, err := loadPinnedWorkflow(ref.Dir)
		if err != nil {
			printLine(stderr, err.Error())
			return exitInternal
		}
		report, err := spec.Validate(pinned.Workflow)
		if err != nil {
			printLine(stderr, err.Error())
			return exitInternal
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
	if exit != exitOK {
		return exit
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

// formatStatusOpaqueJSON is --json output for a run this build cannot replay:
// the documented envelope, with empty values for what cannot be known.
func formatStatusOpaqueJSON(root string, ref *journal.RunRef) string {
	return formatStatusJSON(root, []statusRunJSON{opaqueStatusRunJSON(ref)})
}

// opaqueStatusRunJSON is the runs[] entry for a run this build cannot replay.
// workflow is the workflow-id directory name; workflow_path is the path
// recorded in the run's own plan.json (decoded leniently, so a newer-format
// plan still yields it), or "" when absent or unreadable. The workflow ID is
// never re-resolved as a filename.
func opaqueStatusRunJSON(ref *journal.RunRef) statusRunJSON {
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
		Workflow:     ref.WorkflowID,
		WorkflowPath: workflowPath,
		Status:       ref.StatusText(),
		Visits:       map[string]int{},
		StateKeys:    []string{},
		Reason:       opaqueSentence(ref),
		Soft:         statusSoftJSON{StepIDs: []string{}},
	}
}
