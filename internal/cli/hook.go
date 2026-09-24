package cli

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/dcferreira/agent-pawl/internal/guard"
	"github.com/dcferreira/agent-pawl/internal/hook"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// nowFunc is the clock the enforcement code reads; tests override it.
var nowFunc = time.Now

// cmdHook implements `pawl hook pre|stop`, Claude Code's PreToolUse/Stop
// entry point (enforcement spec): allow = exit 0 silently, deny/block =
// exit 2 with a one-line reason on stderr, fail open = exit 1 (Claude Code
// treats any other non-zero exit as a non-blocking error). Stop never fails
// closed: an error always allows.
func cmdHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "pre" && args[0] != "stop") {
		fmt.Fprintln(stderr, "usage: pawl hook pre|stop  (reads a Claude Code hook payload on stdin)")
		return 2
	}
	event := args[0]
	failOpen := func(msg string) int {
		printLine(stderr, "pawl hook "+event+":", msg)
		if event == "stop" {
			return 0
		}
		return 1
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return failOpen(err.Error())
	}
	p, err := hook.ParsePayload(data)
	if err != nil {
		return failOpen(err.Error())
	}
	root, err := journal.ResolveRoot(p.Cwd)
	if err != nil {
		return failOpen(err.Error())
	}
	if event == "pre" && hook.IsPawlCommand(p.Command) {
		if err := journal.WriteHeartbeat(root, p.SessionID, nowFunc()); err != nil {
			printLine(stderr, "pawl hook pre: writing heartbeat:", err.Error())
		}
	}
	runs, err := loadLiveRuns(root)
	if err != nil {
		return failOpen(err.Error())
	}
	var d hook.Decision
	if event == "pre" {
		d = hook.DecidePre(runs, p)
	} else {
		d = hook.DecideStop(runs, p)
	}
	if d.Allow {
		return 0
	}
	printLine(stderr, d.Reason)
	return 2
}

// loadLiveRuns builds hook.LiveRun values for every live run in root from
// the run directory alone: replayed state (journal.Live), plan.json (guards
// and step kinds) and driver.json.
func loadLiveRuns(root string) ([]hook.LiveRun, error) {
	live, err := journal.Live(root)
	if err != nil {
		return nil, err
	}
	out := make([]hook.LiveRun, 0, len(live))
	for _, ref := range live {
		lr := hook.LiveRun{
			RunID: ref.RunID, WorkflowID: ref.WorkflowID,
			Blocked:    ref.State.Status() == "blocked",
			CursorStep: ref.State.Cursor.Step,
		}
		if !lr.Blocked {
			lr.ActiveSteps = activeSteps(ref.State)
		}
		plan, err := journal.ReadPlan(ref.Dir)
		if err != nil {
			lr.GuardsErr = fmt.Errorf("reading plan.json: %v", err)
		} else if plan.Workflow == nil {
			lr.GuardsErr = fmt.Errorf("reading plan.json: plan.json has no workflow")
		} else {
			if s := plan.Workflow.StepByID(lr.CursorStep); s != nil {
				lr.CursorKind = s.Kind
			}
			if len(plan.Workflow.Guards) > 0 {
				lr.Guards, lr.GuardsErr = guard.Compile(plan.Workflow.Guards)
			}
		}
		if d, ok, err := journal.ReadDriver(ref.Dir); err == nil && ok {
			lr.DriverSession = d.SessionID
		}
		out = append(out, lr)
	}
	return out, nil
}

func activeSteps(rs *journal.RunState) []string {
	steps := []string{rs.Cursor.Step}
	var branches []string
	for _, set := range rs.PendingBranches {
		for id, outstanding := range set {
			if outstanding {
				branches = append(branches, id)
			}
		}
	}
	sort.Strings(branches)
	return append(steps, branches...)
}
