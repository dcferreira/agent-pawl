package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakePawl puts a `pawl` on PATH that records its args and stdin.
func fakePawl(t *testing.T) (pathDir, logFile string) {
	t.Helper()
	return fakePawlScript(t, "exit 7\n")
}

// fakePawlScript is fakePawl with tail as the rest of the script, run after
// the args/stdin have been recorded.
func fakePawlScript(t *testing.T, tail string) (pathDir, logFile string) {
	t.Helper()
	pathDir = t.TempDir()
	logFile = filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\necho \"args:$*\" >> " + logFile + "\ncat >> " + logFile + "\n" + tail
	if err := os.WriteFile(filepath.Join(pathDir, "pawl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return pathDir, logFile
}

func runHookScript(t *testing.T, stateDir, pathDir, event, stdin string) (int, string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "bin", "pawl-hook"), event)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = []string{"PATH=" + pathDir + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "PAWL_STATE_DIR=" + stateDir}
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

func TestPawlHook_FastPathSkipsBinary(t *testing.T) {
	pathDir, logFile := fakePawl(t)
	stateDir := filepath.Join(t.TempDir(), "runs")
	code, _ := runHookScript(t, stateDir, pathDir, "pre", `{"cwd":"/x","tool_input":{"command":"ls"}}`)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Stat(logFile); err == nil {
		t.Fatal("binary invoked on the no-live-run, non-pawl fast path")
	}
}

func TestPawlHook_PawlCommandReachesBinaryWithStdin(t *testing.T) {
	pathDir, logFile := fakePawl(t)
	stateDir := filepath.Join(t.TempDir(), "runs")
	payload := `{"cwd":"/x","tool_input":{"command":"pawl run g"}}`
	code, _ := runHookScript(t, stateDir, pathDir, "pre", payload)
	if code != 1 {
		t.Fatalf("a non-zero binary exit must fail open (exit 1); got %d", code)
	}
	log, _ := os.ReadFile(logFile)
	if !strings.Contains(string(log), "args:hook pre") || !strings.Contains(string(log), payload) {
		t.Fatalf("log=%q", log)
	}
}

func TestPawlHook_LiveRunReachesBinaryForStop(t *testing.T) {
	pathDir, logFile := fakePawl(t)
	base := t.TempDir()
	stateDir := filepath.Join(base, "runs")
	if err := os.MkdirAll(filepath.Join(base, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent", filepath.Join(base, "live", "x__wf__ab12")); err != nil {
		t.Fatal(err)
	}
	runHookScript(t, stateDir, pathDir, "stop", `{"cwd":"/x"}`)
	log, _ := os.ReadFile(logFile)
	if !strings.Contains(string(log), "args:hook stop") {
		t.Fatalf("log=%q", log)
	}
}

func TestPawlHook_BadEventUsage(t *testing.T) {
	pathDir, _ := fakePawl(t)
	// A usage mistake fails open (exit 1), never exit 2 — exit 2 means
	// "block" to Claude Code, and a bad event name is not a considered
	// block decision, and it matches cmdHook's own usage exit.
	if code, _ := runHookScript(t, t.TempDir(), pathDir, "bogus", "{}"); code != 1 {
		t.Fatalf("code=%d", code)
	}
}

// TestPawlHook_MissingBinaryFailsOpenNotBlock: with no real
// `pawl` anywhere on PATH (only this repo's own bin/ directory, which is
// the wrapper script itself, not a real binary), a payload that mentions
// pawl takes the slow path and reaches bin/pawl, whose own recursion/
// missing-binary guard prints an install message and exits 1 — never exit
// 2, which Claude Code would read as "block this tool call".
func TestPawlHook_MissingBinaryFailsOpenNotBlock(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "runs")
	pathDir := filepath.Join(repoRoot(t), "bin")
	cmd := exec.Command(filepath.Join(pathDir, "pawl-hook"), "pre")
	cmd.Stdin = strings.NewReader(`{"cwd":"/x","tool_input":{"command":"pawl run g"}}`)
	cmd.Env = []string{"PATH=" + pathDir + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "PAWL_STATE_DIR=" + stateDir}
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("code=%d, want 1 (fail open, not exit 2/block); output:\n%s", code, out)
	}
	if !strings.Contains(string(out), "go install") {
		t.Fatalf("expected the install message, got:\n%s", out)
	}
}

// TestEnforcedRun_HeartbeatFromRealHookLetsRunStart drives the real, built pawl binary through both
// halves of the enforcement handshake with no shortcuts — a real
// `pawl hook pre` invocation (not internal/hook or internal/cli directly)
// writes the heartbeat for a PreToolUse payload naming `pawl run
// green-tests`, and then a `pawl run green-tests` invoked WITHOUT
// PAWL_ENFORCEMENT=off (unlike every other e2e test's p.run helper) must
// start enforced rather than refuse.
func TestEnforcedRun_HeartbeatFromRealHookLetsRunStart(t *testing.T) {
	p := newProject(t)

	payload := fmt.Sprintf(`{"session_id":"s1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"pawl run green-tests"}}`, p.root)
	hookCmd := exec.Command(wfBinary(t), "hook", "pre")
	hookCmd.Stdin = strings.NewReader(payload)
	hookCmd.Env = append(os.Environ(), "PAWL_STATE_DIR="+p.stateDir)
	if out, err := hookCmd.CombinedOutput(); err != nil {
		t.Fatalf("pawl hook pre: %v, output:\n%s", err, out)
	}

	runCmd := exec.Command(wfBinary(t), "run", "green-tests")
	runCmd.Dir = p.root
	runCmd.Env = append(os.Environ(), "PAWL_STATE_DIR="+p.stateDir) // deliberately no PAWL_ENFORCEMENT=off
	out, err := runCmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("pawl run green-tests: exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(string(out), "hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)") {
		t.Fatalf("expected the enforced banner, got:\n%s", out)
	}
}

// TestPawlHook_OldBinaryFailsOpen: the plugin and the pawl binary update
// separately. A binary built before `pawl hook` existed prints usage and
// exits 2 for the unknown subcommand, and Claude Code reads a bare exit 2 as
// "block" — so the wrapper must map it to exit 1 (fail open), or every Bash
// call mentioning pawl (including the `go install`/`pawl update` that would
// fix the skew) is denied and every Stop in a path containing "pawl" is
// refused.
func TestPawlHook_OldBinaryFailsOpen(t *testing.T) {
	pathDir, _ := fakePawlScript(t, "echo 'usage: pawl <command> ...' >&2\nexit 2\n")
	stateDir := filepath.Join(t.TempDir(), "runs")
	for _, tc := range []struct{ event, payload string }{
		{"pre", `{"cwd":"/x","tool_input":{"command":"go install github.com/dcferreira/agent-pawl/cmd/pawl@latest"}}`},
		{"stop", `{"cwd":"/home/u/agent-pawl"}`},
	} {
		code, out := runHookScript(t, stateDir, pathDir, tc.event, tc.payload)
		if code != 1 {
			t.Fatalf("%s: code=%d, want 1 (fail open, never exit 2/block); output:\n%s", tc.event, code, out)
		}
		if strings.Contains(out, `"decision"`) || strings.Contains(out, "permissionDecision") {
			t.Fatalf("%s: a failing binary must not produce a decision; output:\n%s", tc.event, out)
		}
	}
}

// TestPawlHook_JSONDecisionPassesThrough: a considered deny/block is exit 0
// with Claude Code's JSON decision on stdout; the wrapper relays it intact.
func TestPawlHook_JSONDecisionPassesThrough(t *testing.T) {
	decision := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"pawl: denied"}}`
	pathDir, _ := fakePawlScript(t, "printf '%s\\n' '"+decision+"'\nexit 0\n")
	stateDir := filepath.Join(t.TempDir(), "runs")
	code, out := runHookScript(t, stateDir, pathDir, "pre", `{"cwd":"/x","tool_input":{"command":"pawl run g"}}`)
	if code != 0 || strings.TrimSpace(out) != decision {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
