package cli

import (
	"bytes"
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

func startHookRun(t *testing.T) (root, runID string) {
	t.Helper()
	root = setupWorkingCopy(t)
	t.Setenv("PAWL_ENFORCEMENT", "off")
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

func TestHookPre_GuardDenyExit2(t *testing.T) {
	root, runID := startHookRun(t)
	code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "git push origin main", ""))
	if code != 2 || out != "" || !strings.Contains(errs, "push-in-commit") || !strings.Contains(errs, runID) {
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
	t.Setenv("PAWL_ENFORCEMENT", "off")
	writeWorkflow(t, root, "hookwf", hookWF)
	var out, errb bytes.Buffer
	if code := Run([]string{"pawl", "run", "hookwf"}, &out, &errb); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := hookCall(t, "pre", prePayload(sub, "s1", "git push", "")); code != 2 {
		t.Fatalf("subdirectory cwd must resolve to the run's working copy; code=%d", code)
	}
}

func TestHookPre_SubagentVCSDeny(t *testing.T) {
	root, _ := startHookRun(t)
	if code, _, errs := hookCall(t, "pre", prePayload(root, "s1", "git commit -am x", "agent-1")); code != 2 || !strings.Contains(errs, "subagent") {
		t.Fatalf("code=%d err=%q", code, errs)
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
	code, _, errs := hookCall(t, "stop", stopPayload(root, "s1", false))
	if code != 2 || !strings.Contains(errs, "pawl abandon --run "+runID) {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if code, _, _ := hookCall(t, "stop", stopPayload(root, "s1", true)); code != 0 {
		t.Fatalf("stop_hook_active must allow; code=%d", code)
	}
	if code, _, _ := hookCall(t, "stop", stopPayload(root, "s2", false)); code != 0 {
		t.Fatalf("other session must be allowed; code=%d", code)
	}
}

func TestHookStop_GarbageAllows(t *testing.T) {
	setupWorkingCopy(t)
	if code, _, _ := hookCall(t, "stop", "not json"); code != 0 {
		t.Fatalf("stop must never trap a session; code=%d", code)
	}
}

func TestHook_UnknownEventUsage(t *testing.T) {
	setupWorkingCopy(t)
	if code, _, _ := hookCall(t, "bogus", "{}"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}
