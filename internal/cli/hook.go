package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/dcferreira/agent-pawl/internal/hook"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// nowFunc is the clock the enforcement code reads; tests override it.
var nowFunc = time.Now

// cmdHook implements `pawl hook pre|stop`, Claude Code's PreToolUse/Stop
// entry point (enforcement spec): allow = exit 0 silently; deny/block =
// exit 0 with Claude Code's JSON decision on stdout (writeHookDecision);
// fail open = exit 1 (Claude Code treats any non-zero exit other than 2 as
// a non-blocking error). This command never exits 2. A bare exit 2 is
// indistinguishable from a pawl binary too old to have a `hook` subcommand
// at all (cli.Run's unknown-command usage exit), so bin/pawl-hook maps every
// non-zero exit to fail open and only a positive JSON decision blocks —
// the plugin and the binary update separately, and skew between them must
// never deny tool calls or trap Stop. Stop's own fail-open path stays exit
// 0, since Stop must never trap a session regardless of why it errored.
func cmdHook(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	if len(args) != 1 || (args[0] != "pre" && args[0] != "stop") {
		fmt.Fprintln(stderr, "usage: pawl hook pre|stop  (reads a Claude Code hook payload on stdin)")
		return 1
	}
	event := args[0]
	failOpen := func(msg string) int {
		printLine(stderr, "pawl hook "+event+":", msg)
		if event == "stop" {
			return 0
		}
		return 1
	}
	defer func() {
		if r := recover(); r != nil {
			code = failOpen(fmt.Sprintf("recovered from a panic: %v", r))
		}
	}()
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
	live, err := journal.Live(root)
	if err != nil {
		return failOpen(err.Error())
	}
	// Self-heal: bin/pawl-hook's fast path decides whether to invoke this
	// binary at all from the live/ symlinks alone, never from journal.Live
	// directly. A forgotten symlink — hand-deleted, or never written by a
	// crashed process — would leave that fast path silently taking the "no
	// live run" exit while a run is genuinely live, and a stale one would
	// keep waking it for nothing; this re-derives this slug's entries (and
	// prunes dangling ones of any slug) from the run directories every time
	// this binary is reached, including with zero live runs. Best-effort: a
	// sync failure must not change this call's own allow/deny decision.
	_ = journal.SyncLiveIndex(root)
	if event == "stop" {
		// Stop finds the run this session drives by driver.json's session
		// id, across every working copy: the Bash tool's cwd persists
		// between calls, so a driving session that has cd'd out of its
		// working copy would otherwise resolve to a slug with no runs and
		// walk away mid-dispatch. PreToolUse stays cwd-scoped.
		live = appendDrivenRuns(live, p.SessionID)
	}
	runs := buildLiveRuns(live)
	var d hook.Decision
	if event == "pre" {
		d = hook.DecidePre(runs, p)
	} else {
		d = hook.DecideStop(runs, p)
	}
	if d.Allow {
		return 0
	}
	if err := writeHookDecision(stdout, event, d.Reason); err != nil {
		return failOpen(err.Error())
	}
	return 0
}

// writeHookDecision writes Claude Code's JSON decision for a deny (pre) or
// block (stop) with reason, the whole of stdout for this invocation.
func writeHookDecision(w io.Writer, event, reason string) error {
	var v any
	if event == "pre" {
		v = map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		}}
	} else {
		v = map[string]string{"decision": "block", "reason": reason}
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// appendDrivenRuns adds to live every run in the live/ index (verified
// through the journal by journal.LiveIndexed) whose driver.json names
// sessionID, skipping any already in live. An index read error adds nothing:
// Stop never fails closed.
func appendDrivenRuns(live []journal.RunRef, sessionID string) []journal.RunRef {
	if sessionID == "" {
		return live
	}
	indexed, err := journal.LiveIndexed()
	if err != nil {
		return live
	}
	seen := make(map[string]bool, len(live))
	for _, r := range live {
		seen[r.Dir] = true
	}
	for _, r := range indexed {
		if seen[r.Dir] {
			continue
		}
		if d, ok, err := journal.ReadDriver(r.Dir); err == nil && ok && d.SessionID == sessionID {
			seen[r.Dir] = true
			live = append(live, r)
		}
	}
	return live
}

// buildLiveRuns builds hook.LiveRun values for live runs from the run
// directory alone: replayed state, plan.json (guards and step kinds) and
// driver.json. A run started with enforcement opted out
// (journal.RunState.EnforcementOff) is left out entirely, so neither
// DecidePre nor DecideStop ever acts on it — its banner said its guards are
// NOT enforced, and nothing is checked for it.
func buildLiveRuns(live []journal.RunRef) []hook.LiveRun {
	out := make([]hook.LiveRun, 0, len(live))
	for _, ref := range live {
		if ref.State.EnforcementOff() {
			continue
		}
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
				lr.Guards, lr.GuardsErr = hook.CompileGuards(plan.Workflow.Guards)
			}
		}
		if d, ok, err := journal.ReadDriver(ref.Dir); err == nil && ok {
			lr.DriverSession = d.SessionID
		}
		out = append(out, lr)
	}
	return out
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
