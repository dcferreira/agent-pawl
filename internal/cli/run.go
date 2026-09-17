package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dcferreira/agentic-workflow-fsm/internal/engine"
	"github.com/dcferreira/agentic-workflow-fsm/internal/journal"
	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// cmdRun implements wf run <name> [key=value …] [--fresh] [--force]
// [--run <id>] (design/format-spec.md §I): resolve the workflow, gate on
// spec.Validate, resume a live run when exactly one resolves (--run only
// disambiguates), or bind args and start a fresh one, then print the start
// banner and the first instruction line.
func cmdRun(args []string, cwd string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "wf run: missing workflow name")
		fmt.Fprintln(stderr, "usage: wf run <name> [key=value …] [--fresh] [--force] [--run <id>]")
		return 2
	}
	name := args[0]
	raw, flags, err := parseRunArgs(args[1:])
	if err != nil {
		printLine(stderr, err.Error())
		return 2
	}
	if flags.Fresh && flags.RunID != "" {
		fmt.Fprintln(stderr, "wf run: --run is only used to disambiguate a resume; it does not name a fresh run's id — drop --run or drop --fresh")
		return 2
	}

	rw, err := resolveWorkflowFile(cwd, name)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}
	w, report, err := loadAndValidate(rw.Path)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}
	if len(report.Errors) > 0 {
		errBlock := &blockWriter{}
		for _, e := range report.Errors {
			errBlock.line(0, e)
		}
		fmt.Fprint(stderr, errBlock.String())
		return 1
	}

	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}

	e := engine.New(w, root)
	fmt.Fprint(stdout, formatBanner(rw, report))

	if !flags.Fresh {
		ref, rerr := resolveRunToResume(root, w.Workflow, flags.RunID)
		if rerr != nil {
			printLine(stderr, rerr.Error())
			return 1
		}
		if ref != nil {
			if len(raw) > 0 {
				// I1: args are bound once, at RUN_START, and are read-only
				// state thereafter (design/format-spec.md §B.9). Silently
				// dropping a key=value passed alongside a resume is exactly
				// the "hide a typo" failure Global Constraint 5 forbids —
				// bogus=1 on a resume must be refused, not swallowed.
				// formatArgsKV's join has no format validator behind it any
				// more than a state key name does (fix round 5's finding:
				// this used to reach stderr via a bare Fprintf, and a raw CR
				// in a bound arg's value re-homed the cursor to column 0).
				printLine(stderr, fmt.Sprintf("wf run: args are bound at run start; run %s was started with %s — use --fresh to rebind", ref.RunID, formatArgsKV(ref.State.Args)))
				return 2
			}
			instr, ierr := e.Resume(ref.RunID, flags.Force)
			if ierr != nil {
				// M1: the resume line is only printed once Resume has
				// actually succeeded — printing it first made a
				// digest-mismatch refusal read as though the run had
				// resumed.
				return handleRunErr(ierr, stderr, root, w, ref.RunID)
			}
			fmt.Fprint(stdout, formatResumeLine(ref.RunID, ref.State.Cursor.Step, ref.State.Cursor.Attempt, ref.State.State))
			fmt.Fprint(stdout, formatInstruction(instr, w, root))
			return 0
		}
	}

	boundArgs, err := bindArgs(w.Args, raw)
	if err != nil {
		var ue *argsUsageError
		if errors.As(err, &ue) {
			b := &blockWriter{}
			b.line(0, ue.Reason)
			writeArgsUsage(b, ue.Decls)
			fmt.Fprint(stderr, b.String())
			return 2
		}
		printLine(stderr, err.Error())
		return 2
	}
	runID := newRunID()
	instr, err := e.Start(runID, boundArgs)
	if err != nil {
		printLine(stderr, "wf run:", err.Error())
		return 1
	}
	fmt.Fprint(stdout, formatInstruction(instr, w, root))
	return 0
}

// resolveRunToResume applies design/format-spec.md §I / DESIGN.md §4's
// resume rule for workflowID's runs under root: resume applies when exactly
// one non-terminal run resolves; explicit only disambiguates among several.
// It returns (nil, nil) when there is nothing to resume, so the caller
// starts fresh.
func resolveRunToResume(root, workflowID, explicit string) (*journal.RunRef, error) {
	live, err := journal.Live(root)
	if err != nil {
		return nil, err
	}
	var matches []journal.RunRef
	for _, r := range live {
		if r.WorkflowID == workflowID {
			matches = append(matches, r)
		}
	}
	if explicit != "" {
		for i := range matches {
			if matches[i].RunID == explicit {
				return &matches[i], nil
			}
		}
		return nil, fmt.Errorf("wf run: no live run %q for workflow %q", explicit, workflowID)
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return &matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.RunID
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("wf run: multiple runs are live for workflow %q; disambiguate with --run <id>: %s", workflowID, strings.Join(ids, ", "))
	}
}

// handleRunErr formats a Resume failure: a digest mismatch names the steps
// that changed and offers --fresh (DESIGN.md §4 step 2); anything else is
// printed as given.
func handleRunErr(err error, stderr io.Writer, root string, w *spec.Workflow, runID string) int {
	if errors.Is(err, engine.ErrDigestMismatch) {
		changed := diffChangedSteps(root, w, runID)
		printLine(stderr, fmt.Sprintf("wf run: workflow file has changed since run %s started (changed: %s); use --fresh to start a new run", runID, changed))
		return 1
	}
	printLine(stderr, "wf run:", err.Error())
	return 1
}

// diffChangedSteps compares the workflow plan.json recorded when runID
// started against w (the just-recompiled workflow), naming which steps
// differ, via the diffSteps helper shared with loadPinnedWorkflow.
func diffChangedSteps(root string, w *spec.Workflow, runID string) string {
	dir := journal.RunDir(root, w.Workflow, runID)
	plan, err := journal.ReadPlan(dir)
	if err != nil || plan.Workflow == nil {
		return "(unable to determine; compare the workflow file against your last edit)"
	}
	return diffSteps(plan.Workflow, w)
}
