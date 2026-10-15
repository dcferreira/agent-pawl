package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/dcferreira/agent-pawl/internal/hook"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// nowFunc is the clock the enforcement code reads; tests override it.
var nowFunc = time.Now

// cmdHook implements `pawl hook pre|stop|subagent-stop`, Claude Code's
// PreToolUse/Stop/SubagentStop entry point (enforcement spec): allow = exit 0 silently; deny/block =
// exit 0 with Claude Code's JSON decision on stdout (writeHookDecision);
// fail open = exit 1 (Claude Code treats any non-zero exit other than 2 as
// a non-blocking error). This command never exits 2. A bare exit 2 is
// indistinguishable from a pawl binary too old to have a `hook` subcommand
// at all (cli.Run's unknown-command usage exit), so bin/pawl-hook maps every
// non-zero exit to fail open and only a positive JSON decision blocks —
// the plugin and the binary update separately, and skew between them must
// never deny tool calls or trap Stop. Stop's own fail-open path stays exit
// 0, since Stop must never trap a session regardless of why it errored.
//
// subagent-stop never decides anything and never writes stdout: when the
// payload's session drives a live, enforced run it records a marker
// (journal.WriteSubagentStop), which a later Stop that would block may spend
// once (hook.DecideStop) — Claude Code fires SubagentStop and drops the task
// from background_tasks while its completion notification is still queued,
// so that Stop would otherwise refuse a turn the session is about to be
// woken for. Like Stop it fails open with exit 0: exit 2 would block the
// subagent from stopping.
func cmdHook(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// Help is the one thing that is not a hook call. Everything else keeps
	// the exact event check below, and its usage error stays exit 1: hooks
	// never exit 2, which Claude Code reads as "block".
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprint(stdout, cmdUsage(usageHook))
			return hookExitOK
		}
	}
	if len(args) != 1 || (args[0] != "pre" && args[0] != "stop" && args[0] != "subagent-stop") {
		fmt.Fprintln(stderr, "usage: pawl hook pre|stop|subagent-stop  (reads a Claude Code hook payload on stdin)")
		return hookExitFailOpen
	}
	event := args[0]
	failOpen := func(msg string) int {
		printLine(stderr, "pawl hook "+event+":", msg)
		if event != "pre" {
			return hookExitOK
		}
		return hookExitFailOpen
	}
	defer func() {
		if r := recover(); r != nil {
			code = failOpen(fmt.Sprintf("recovered from a panic: %v", r))
		}
	}()
	if commandHook != nil {
		commandHook("hook")
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
		// The hook always runs through the plugin's own bin/pawl-hook and
		// launcher, so this env is the version of the plugin whose hooks are
		// bound: recorded so pawl run can treat it as authoritative.
		pluginVersion := os.Getenv(envPluginVersion)
		if err := journal.WriteHeartbeatWithPlugin(root, p.SessionID, pluginVersion, nowFunc()); err != nil {
			printLine(stderr, "pawl hook pre: writing heartbeat:", err.Error())
		}
		// Also keyed by session: pawl run/submit/poll may run in a working
		// copy other than this payload's cwd (issue #18).
		if err := journal.WriteSessionHeartbeatWithPlugin(p.SessionID, pluginVersion, nowFunc()); err != nil {
			printLine(stderr, "pawl hook pre: writing session heartbeat:", err.Error())
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
	// Both events find the runs this session drives by driver.json's
	// session id, across every working copy. Stop: the Bash tool's cwd
	// persists between calls, so a driving session that has cd'd out of its
	// working copy would otherwise resolve to a slug with no runs and walk
	// away mid-dispatch. PreToolUse: the payload cwd is always the session's
	// start directory, so a run in a working copy the session cd'd into
	// would otherwise never be enforced (issue #18).
	live = appendDrivenRuns(live, p.SessionID)
	runs := buildLiveRuns(live)
	var d hook.Decision
	switch event {
	case "pre":
		d = hook.DecidePre(runs, p)
	case "subagent-stop":
		for _, r := range runs {
			// Only a run awaiting this session's `pawl submit` earns credit:
			// a blocked run, or one at a wait/human cursor, never blocks a Stop.
			if !r.Opaque && r.DriverSession != "" && r.DriverSession == p.SessionID && !r.Blocked &&
				(r.CursorKind == "agentic" || r.CursorKind == "parallel") {
				if err := journal.WriteSubagentStop(p.SessionID, p.AgentID, nowFunc(), hook.SubagentStopCreditTTL); err != nil {
					printLine(stderr, "pawl hook subagent-stop: recording marker:", err.Error())
				}
				break
			}
		}
		return hookExitOK
	default:
		d = stopDecision(runs, p, stderr)
	}
	if d.Allow {
		return hookExitOK
	}
	if err := writeHookDecision(stdout, event, d.Reason); err != nil {
		return failOpen(err.Error())
	}
	return hookExitOK
}

// stopDecision runs hook.DecideStop with the session's fresh SubagentStop
// markers, then deletes every one it reports consumed (all of them, whatever
// the decision) and prunes expired ones. Marker
// I/O is best-effort: a read error means no markers (so no credit), and a
// delete or prune failure never changes the decision.
func stopDecision(runs []hook.LiveRun, p hook.Payload, stderr io.Writer) hook.Decision {
	now := nowFunc()
	var markers []hook.StopMarker
	stored, err := journal.ListSubagentStops(p.SessionID, now, hook.SubagentStopCreditTTL)
	if err != nil {
		printLine(stderr, "pawl hook stop: reading subagent-stop markers:", err.Error())
	}
	for _, m := range stored {
		markers = append(markers, hook.StopMarker{ID: m.ID, SessionID: m.SessionID, Time: m.Time})
	}
	d := hook.DecideStop(runs, p, markers, now)
	for _, c := range d.Consumed {
		if err := journal.DeleteSubagentStop(p.SessionID, c.ID); err != nil {
			printLine(stderr, "pawl hook stop: consuming subagent-stop marker:", err.Error())
		}
	}
	_ = journal.PruneSubagentStops(now, hook.SubagentStopCreditTTL)
	return d
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
// through the journal by journal.LiveIndexedDrivenBy) whose driver.json names
// sessionID, skipping any already in live. An index read error adds nothing:
// the hooks never fail closed on it.
func appendDrivenRuns(live []journal.RunRef, sessionID string) []journal.RunRef {
	if sessionID == "" {
		return live
	}
	indexed, err := journal.LiveIndexedDrivenBy(sessionID)
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
		seen[r.Dir] = true
		live = append(live, r)
	}
	return live
}

// buildLiveRuns builds hook.LiveRun values for live runs from the run
// directory alone: replayed state, plan.json (guards and step kinds) and
// driver.json. An opaque run (journal.RunRef.Opaque) becomes a minimal
// LiveRun with Opaque set. A run started with enforcement opted out
// (journal.RunState.EnforcementOff) is left out entirely, so neither
// DecidePre nor DecideStop ever acts on it — its banner said its guards are
// NOT enforced, and nothing is checked for it.
func buildLiveRuns(live []journal.RunRef) []hook.LiveRun {
	out := make([]hook.LiveRun, 0, len(live))
	for _, ref := range live {
		if ref.Opaque != nil {
			// Unreadable journal: no State, plan or cursor to consult, and
			// hook_self_test can't be read, so it is not dropped as
			// enforcement-off. DecidePre/DecideStop give it only the
			// subagent VCS rule.
			lr := hook.LiveRun{RunID: ref.RunID, WorkflowID: ref.WorkflowID, Opaque: true, OpaqueStatus: ref.StatusText()}
			if lr.RunID == "" {
				lr.RunID = ref.Opaque.RunID
			}
			if d, ok, err := journal.ReadDriver(ref.Dir); err == nil && ok {
				lr.DriverSession = d.SessionID
			}
			out = append(out, lr)
			continue
		}
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
