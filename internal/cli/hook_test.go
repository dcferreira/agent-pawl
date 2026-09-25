package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

const hookWF = `workflow: hookwf
start: fix
state:
  done:
    type: string
    default: ""
steps:
  - id: fix
    kind: agentic
    description: "fix it"
    writes:
      done: {type: string}
    postcondition: {all_set: [done]}
    next: done
terminal:
  done: {status: ok, message: "ok"}
guards:
  - id: push-in-commit
    match: "git push"
    only_in: []
`

// startHookRun starts an enforced hookwf run (a fresh heartbeat from session
// s1, so driver.json names s1): an opted-out run is invisible to the hooks.
func startHookRun(t *testing.T) (root, runID string) {
	t.Helper()
	root = setupWorkingCopy(t)
	t.Setenv("PAWL_ENFORCEMENT", "") // undo setupWorkingCopy's opt-out
	if err := journal.WriteHeartbeat(root, "s1", nowFunc()); err != nil {
		t.Fatal(err)
	}
	writeWorkflow(t, root, "hookwf", hookWF)
	var out, errb bytes.Buffer
	if code := Run([]string{"pawl", "run", "hookwf"}, &out, &errb); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	live, err := journal.Live(root)
	if err != nil || len(live) != 1 {
		t.Fatalf("live: %v %v", live, err)
	}
	return root, live[0].RunID
}

func hookCall(t *testing.T, event string, payload string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := RunIO([]string{"pawl", "hook", event}, strings.NewReader(payload), &out, &errb)
	return code, out.String(), errb.String()
}

func prePayload(cwd, session, cmd, agent string) string {
	a := ""
	if agent != "" {
		a = fmt.Sprintf(`,"agent_id":%q`, agent)
	}
	return fmt.Sprintf(`{"session_id":%q,"cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":%q}%s}`, session, cwd, cmd, a)
}

func TestHookPre_NoRunAllowsSilently(t *testing.T) {
	root := setupWorkingCopy(t)
	code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "ls", ""))
	if code != 0 || out != "" || errs != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestHookPre_WritesHeartbeatForPawlCommand(t *testing.T) {
	root := setupWorkingCopy(t)
	if code, _, _ := hookCall(t, "pre", prePayload(root, "s1", "pawl run x", "")); code != 0 {
		t.Fatalf("code=%d", code)
	}
	hb, ok, err := journal.ReadHeartbeat(root)
	if err != nil || !ok || hb.SessionID != "s1" {
		t.Fatalf("hb=%+v ok=%v err=%v", hb, ok, err)
	}
}

func TestHookPre_NoHeartbeatForOtherCommands(t *testing.T) {
	root := setupWorkingCopy(t)
	hookCall(t, "pre", prePayload(root, "s1", "ls", ""))
	if _, ok, _ := journal.ReadHeartbeat(root); ok {
		t.Fatal("heartbeat written for a non-pawl command")
	}
}

// preDenyReason decodes a PreToolUse deny decision: exit 0 with Claude
// Code's JSON decision on stdout (never exit 2, which bin/pawl-hook maps to
// fail open, since a too-old pawl binary also exits 2 for an unknown
// command). ok is false when out is not a deny decision.
func preDenyReason(t *testing.T, code int, out string) (reason string, ok bool) {
	t.Helper()
	if code != 0 || out == "" {
		return "", false
	}
	var v struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("stdout is not a JSON decision: %v: %q", err, out)
	}
	h := v.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || h.PermissionDecision != "deny" || h.PermissionDecisionReason == "" {
		return "", false
	}
	return h.PermissionDecisionReason, true
}

// stopBlockReason decodes a Stop block decision: exit 0 with
// {"decision":"block","reason":…} on stdout.
func stopBlockReason(t *testing.T, code int, out string) (reason string, ok bool) {
	t.Helper()
	if code != 0 || out == "" {
		return "", false
	}
	var v struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("stdout is not a JSON decision: %v: %q", err, out)
	}
	if v.Decision != "block" || v.Reason == "" {
		return "", false
	}
	return v.Reason, true
}

func TestHookPre_GuardDenyJSON(t *testing.T) {
	root, runID := startHookRun(t)
	code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "git push origin main", ""))
	reason, ok := preDenyReason(t, code, out)
	if !ok || errs != "" || !strings.Contains(reason, "push-in-commit") || !strings.Contains(reason, runID) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestHookPre_SubdirCwd(t *testing.T) {
	root := setupWorkingCopy(t)
	// Create the working-copy marker before starting the run, so the run's
	// own root resolution (journal.ResolveRoot, called by cmdRun) and the
	// hook's later resolution from a subdirectory both walk up to and
	// symlink-resolve the exact same marker directory. Doing this after
	// the run starts risks the two resolving to different symlink forms
	// of the same physical directory on a system where TempDir()'s path
	// is itself a symlink (e.g. /tmp on macOS).
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAWL_ENFORCEMENT", "") // an opted-out run is invisible to the hooks
	if err := journal.WriteHeartbeat(root, "s1", nowFunc()); err != nil {
		t.Fatal(err)
	}
	writeWorkflow(t, root, "hookwf", hookWF)
	var out, errb bytes.Buffer
	if code := Run([]string{"pawl", "run", "hookwf"}, &out, &errb); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := hookCall(t, "pre", prePayload(sub, "s1", "git push", "")); !isPreDeny(t, code, out) {
		t.Fatalf("subdirectory cwd must resolve to the run's working copy; code=%d out=%q", code, out)
	}
}

func TestHookPre_SubagentVCSDeny(t *testing.T) {
	root, _ := startHookRun(t)
	code, out, _ := hookCall(t, "pre", prePayload(root, "s1", "git commit -am x", "agent-1"))
	if reason, ok := preDenyReason(t, code, out); !ok || !strings.Contains(reason, "subagent") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHookPre_GarbagePayloadFailsOpen(t *testing.T) {
	setupWorkingCopy(t)
	code, _, errs := hookCall(t, "pre", "not json")
	if code != 1 || errs == "" {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func stopPayload(cwd, session string, active bool) string {
	return fmt.Sprintf(`{"session_id":%q,"cwd":%q,"hook_event_name":"Stop","stop_hook_active":%v}`, session, cwd, active)
}

func TestHookStop_BlocksDriverAtAgentic(t *testing.T) {
	root, runID := startHookRun(t)
	dir := journal.RunDir(root, "hookwf", runID)
	if err := journal.WriteDriver(dir, "s1", nowFunc()); err != nil {
		t.Fatal(err)
	}
	code, out, errs := hookCall(t, "stop", stopPayload(root, "s1", false))
	if reason, ok := stopBlockReason(t, code, out); !ok || errs != "" || !strings.Contains(reason, "pawl abandon --run "+runID) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, _ := hookCall(t, "stop", stopPayload(root, "s1", true)); code != 0 || out != "" {
		t.Fatalf("stop_hook_active must allow; code=%d out=%q", code, out)
	}
	if code, out, _ := hookCall(t, "stop", stopPayload(root, "s2", false)); code != 0 || out != "" {
		t.Fatalf("other session must be allowed; code=%d out=%q", code, out)
	}
}

func TestHookStop_AllowsWhileBackgroundWorkInFlight(t *testing.T) {
	root, runID := startHookRun(t)
	dir := journal.RunDir(root, "hookwf", runID)
	if err := journal.WriteDriver(dir, "s1", nowFunc()); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"session_id":"s1","cwd":%q,"hook_event_name":"Stop","stop_hook_active":false,`+
		`"background_tasks":[{"id":"t1","type":"subagent","status":"running","description":"fix tests"}]}`, root)
	code, out, errs := hookCall(t, "stop", payload)
	if code != 0 || out != "" || errs != "" {
		t.Fatalf("driver at agentic with a background task must allow silently; code=%d out=%q err=%q", code, out, errs)
	}
}

// TestHookPre_SelfHealsMissingLiveIndexEntry: `pawl hook pre|stop` never syncs the live index through cli.go's shared
// post-dispatch step (it returns early), so a forgotten symlink — deleted by
// hand, or by a crashed process that never reached the sync — would strand
// bin/pawl-hook's fast path in "no live run" forever without this. Once
// cmdHook has successfully loaded at least one live run, it must
// best-effort SyncLiveIndex so the next fast-path check heals itself.
func TestHookPre_SelfHealsMissingLiveIndexEntry(t *testing.T) {
	root, runID := startHookRun(t)
	link := filepath.Join(journal.LiveIndexDir(), journal.Slug(root)+"__hookwf__"+runID)
	if err := os.Remove(link); err != nil {
		t.Fatalf("removing the live index entry to simulate a forgotten one: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("precondition: link should be gone, err=%v", err)
	}
	if code, _, _ := hookCall(t, "pre", prePayload(root, "s1", "ls", "")); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("cmdHook did not self-heal the live index entry: %v", err)
	}
}

func isPreDeny(t *testing.T, code int, out string) bool {
	t.Helper()
	_, ok := preDenyReason(t, code, out)
	return ok
}

func TestHookStop_GarbageAllows(t *testing.T) {
	setupWorkingCopy(t)
	if code, _, _ := hookCall(t, "stop", "not json"); code != 0 {
		t.Fatalf("stop must never trap a session; code=%d", code)
	}
}

func TestHook_UnknownEventUsage(t *testing.T) {
	setupWorkingCopy(t)
	// A usage mistake fails open (exit 1), never exit 2: exit 2 means
	// "block" to Claude Code, and an unrecognized event is not a considered
	// block decision, and bin/pawl-hook's usage exit is 1 too.
	if code, _, _ := hookCall(t, "bogus", "{}"); code != 1 {
		t.Fatalf("code=%d", code)
	}
}

// panicReader panics on Read, so a real panic is raised inside cmdHook and
// its real deferred recover() handles it.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("boom") }

// TestCmdHook_PanicFailsOpen: a panic anywhere inside cmdHook's
// decision path must not surface as exit 2 (which Claude Code reads as
// "block") or crash the process — it must fail open exactly like any other
// internal error, pre -> exit 1, stop -> exit 0.
func TestCmdHook_PanicFailsOpen(t *testing.T) {
	setupWorkingCopy(t)
	for _, tc := range []struct {
		event string
		want  int
	}{{"pre", 1}, {"stop", 0}} {
		var out, errb bytes.Buffer
		code := RunIO([]string{"pawl", "hook", tc.event}, panicReader{}, &out, &errb)
		if code != tc.want || out.String() != "" || !strings.Contains(errb.String(), "recovered from a panic: boom") {
			t.Fatalf("%s: code=%d out=%q err=%q", tc.event, code, out.String(), errb.String())
		}
	}
}

// TestHookPre_NonBashToolAllowedDespiteBrokenGuards: a non-Bash tool call
// has no command for any rule to classify, so even a run whose guards can't
// be loaded (every Bash command fails closed) must not deny it.
func TestHookPre_NonBashToolAllowedDespiteBrokenGuards(t *testing.T) {
	root, runID := startHookRun(t)
	planPath := filepath.Join(journal.RunDir(root, "hookwf", runID), "plan.json")
	if err := os.WriteFile(planPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := hookCall(t, "pre", prePayload(root, "s1", "ls", "")); code != 0 || !strings.Contains(out, "deny") {
		t.Fatalf("precondition: Bash must fail closed; code=%d out=%q", code, out)
	}
	payload := fmt.Sprintf(`{"session_id":"s1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"x"}}`, root)
	if code, out, errs := hookCall(t, "pre", payload); code != 0 || out != "" {
		t.Fatalf("non-Bash tool denied: code=%d out=%q err=%q", code, out, errs)
	}
}

// The Stop hook finds the driven run by driver.json's session id across
// every working copy, not through the payload cwd's slug: a driving session
// that has cd'd out of its working copy still owes the run a submit.
func TestHookStop_BlocksDriverWithCwdOutsideWorkingCopy(t *testing.T) {
	root, runID := startHookRun(t)
	if err := journal.WriteDriver(journal.RunDir(root, "hookwf", runID), "s1", nowFunc()); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	code, out, errs := hookCall(t, "stop", stopPayload(elsewhere, "s1", false))
	if reason, ok := stopBlockReason(t, code, out); !ok || !strings.Contains(reason, "pawl abandon --run "+runID) {
		t.Fatalf("driver outside its working copy must still be blocked; code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, _ := hookCall(t, "stop", stopPayload(elsewhere, "s2", false)); code != 0 || out != "" {
		t.Fatalf("another session must be allowed; code=%d out=%q", code, out)
	}
	// PreToolUse stays cwd-scoped: the guard does not apply elsewhere.
	if code, out, _ := hookCall(t, "pre", prePayload(elsewhere, "s1", "git push", "")); code != 0 || out != "" {
		t.Fatalf("pre must stay cwd-scoped; code=%d out=%q", code, out)
	}
}

// With zero live runs for the cwd's slug, cmdHook still syncs the live
// index, pruning that slug's stale entries.
func TestHookPre_PrunesStaleLiveIndexWithNoRuns(t *testing.T) {
	root := setupWorkingCopy(t)
	if err := os.MkdirAll(journal.LiveIndexDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(journal.LiveIndexDir(), journal.Slug(root)+"__hookwf__dead")
	if err := os.Symlink(t.TempDir(), stale); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := hookCall(t, "pre", prePayload(root, "s1", "ls", "")); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale live index entry survived: err=%v", err)
	}
}

// TestHookPre_UncompilableGuardFailsClosed: guard.Compile returns a nil
// *Table on error, and a nil table's Denied allows everything — so a run
// whose plan.json carries a guard that fails to compile must never have
// such a table consulted. It must fail closed instead: every non-pawl Bash
// command is denied, naming the run, even one no guard would ever match.
// plan.json is rewritten directly because spec.Validate would reject the
// bad pattern before any run could start with it.
func TestHookPre_UncompilableGuardFailsClosed(t *testing.T) {
	root, runID := startHookRun(t)
	planPath := filepath.Join(journal.RunDir(root, "hookwf", runID), "plan.json")
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan journal.Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Workflow == nil || len(plan.Workflow.Guards) != 1 {
		t.Fatalf("precondition: plan.json should carry hookwf's one guard: %s", raw)
	}
	plan.Workflow.Guards[0].Match = "(" // not a valid RE2 regexp
	raw, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"ls", "echo hello"} {
		code, out, errs := hookCall(t, "pre", prePayload(root, "s1", cmd, ""))
		reason, ok := preDenyReason(t, code, out)
		if !ok || !strings.Contains(reason, runID) || !strings.Contains(reason, "could not be loaded") {
			t.Fatalf("%q: want a fail-closed deny naming run %s; code=%d out=%q err=%q", cmd, runID, code, out, errs)
		}
	}
	// The pawl-only exemption still lets the driver abandon the run.
	if code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "pawl abandon --run "+runID, "")); code != 0 || strings.Contains(out, "deny") {
		t.Fatalf("pawl abandon must stay allowed; code=%d out=%q err=%q", code, out, errs)
	}
}
