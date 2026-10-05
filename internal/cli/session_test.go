package cli

import (
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// twoWorkingCopies returns working copy A (where the Claude Code session
// started, so the hook payload's cwd) and B (where the session cd'd to and
// where the workflow lives). cwd is B, enforcement on.
func twoWorkingCopies(t *testing.T) (a, b string) {
	t.Helper()
	a = setupWorkingCopy(t)
	t.Setenv("PAWL_ENFORCEMENT", "")
	b = t.TempDir()
	writeWorkflow(t, b, "hookwf", hookWF)
	t.Chdir(b)
	return a, b
}

func TestHookPre_WritesSessionHeartbeat(t *testing.T) {
	root := setupWorkingCopy(t)
	if code, _, _ := hookCall(t, "pre", prePayload(root, "sess-9", "pawl run x", "")); code != 0 {
		t.Fatalf("code=%d", code)
	}
	hb, ok, err := journal.ReadSessionHeartbeat("sess-9")
	if err != nil || !ok || hb.SessionID != "sess-9" {
		t.Fatalf("hb=%+v ok=%v err=%v", hb, ok, err)
	}
	hookCall(t, "pre", prePayload(root, "sess-8", "ls", ""))
	if _, ok, _ := journal.ReadSessionHeartbeat("sess-8"); ok {
		t.Fatal("session heartbeat written for a non-pawl command")
	}
}

func TestRun_FindsSessionHeartbeatAcrossWorkingCopies(t *testing.T) {
	a, b := twoWorkingCopies(t)
	hookCall(t, "pre", prePayload(a, "sX", "cd "+b+" && pawl run hookwf", ""))
	t.Setenv(envSessionID, "sX")
	code, out, errs := runPawl("run", "hookwf")
	if code != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	live, _ := journal.Live(b)
	if len(live) != 1 {
		t.Fatalf("live=%v", live)
	}
	if d, ok, _ := journal.ReadDriver(live[0].Dir); !ok || d.SessionID != "sX" {
		t.Fatalf("driver=%+v ok=%v", d, ok)
	}
}

func TestRun_NoSessionEnvFallsBackToPerRoot(t *testing.T) {
	a, _ := twoWorkingCopies(t)
	hookCall(t, "pre", prePayload(a, "sX", "pawl run hookwf", ""))
	// env var unset (""): only the per-root heartbeat, for A, exists.
	if code, _, _ := runPawl("run", "hookwf"); code != 4 {
		t.Fatalf("code=%d, want refusal", code)
	}
}

func TestRun_StaleSessionHeartbeatFallsBackToPerRoot(t *testing.T) {
	_, b := twoWorkingCopies(t)
	if err := journal.WriteSessionHeartbeat("sX", time.Now().Add(-6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteHeartbeat(b, "s-root", time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envSessionID, "sX")
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	live, _ := journal.Live(b)
	if d, ok, _ := journal.ReadDriver(live[0].Dir); !ok || d.SessionID != "s-root" {
		t.Fatalf("driver=%+v ok=%v, want per-root s-root", d, ok)
	}
}

func TestHookPre_DeniesForSessionRunInOtherWorkingCopy(t *testing.T) {
	a, _ := twoWorkingCopies(t)
	hookCall(t, "pre", prePayload(a, "sX", "pawl run hookwf", ""))
	t.Setenv(envSessionID, "sX")
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("run: %d %s", code, errs)
	}
	code, out, _ := hookCall(t, "pre", prePayload(a, "sX", "git push origin main", ""))
	if reason, ok := preDenyReason(t, code, out); !ok || reason == "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, out, _ := hookCall(t, "pre", prePayload(a, "other", "git push origin main", "")); code != 0 || out != "" {
		t.Fatalf("other session must be allowed: code=%d out=%q", code, out)
	}
}

func TestSubmit_StampsDriverFromSessionHeartbeatAcrossWorkingCopies(t *testing.T) {
	a, b := twoWorkingCopies(t)
	hookCall(t, "pre", prePayload(a, "s1", "pawl run hookwf", ""))
	t.Setenv(envSessionID, "s1")
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("run: %d %s", code, errs)
	}
	live, _ := journal.Live(b)
	// A new session (started in A) picks the run up from B.
	hookCall(t, "pre", prePayload(a, "s2", "pawl submit", ""))
	t.Setenv(envSessionID, "s2")
	if code, _, errs := runPawl("submit", "--run", live[0].RunID, "--step", "fix", "--json", `{"done": "true"}`); code != 0 {
		t.Fatalf("submit: %d %s", code, errs)
	}
	if d, ok, _ := journal.ReadDriver(live[0].Dir); !ok || d.SessionID != "s2" {
		t.Fatalf("driver=%+v ok=%v", d, ok)
	}
}

func TestResume_RestampsFromSessionHeartbeatAcrossWorkingCopies(t *testing.T) {
	a, b := twoWorkingCopies(t)
	hookCall(t, "pre", prePayload(a, "s1", "pawl run hookwf", ""))
	t.Setenv(envSessionID, "s1")
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("run: %d %s", code, errs)
	}
	hookCall(t, "pre", prePayload(a, "s2", "pawl run hookwf", ""))
	t.Setenv(envSessionID, "s2")
	if code, out, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("resume: %d %s %s", code, out, errs)
	}
	live, _ := journal.Live(b)
	if d, ok, _ := journal.ReadDriver(live[0].Dir); !ok || d.SessionID != "s2" {
		t.Fatalf("driver=%+v ok=%v", d, ok)
	}
}
