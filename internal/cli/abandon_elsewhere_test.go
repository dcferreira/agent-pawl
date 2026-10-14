package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// seedRun writes a run directly into root's slug under StateBase: RUN_START
// (enforcement on), a STEP_ENTER, and, when endStatus is non-empty, a RUN_END
// with that status. It is how these tests get the same run id in two working
// copies (ids are random under `pawl run`) and runs in a given state.
func seedRun(t *testing.T, root, wf, id, endStatus string) string {
	t.Helper()
	dir, err := journal.CreateRunDir(root, wf, id)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := journal.OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	evs := []journal.Event{
		{Kind: journal.KindRunStart, RunID: id, HookSelfTest: "on (PreToolUse heartbeat)"},
		{Kind: journal.KindStepEnter, RunID: id, Step: "fix", Attempt: 1},
	}
	if endStatus != "" {
		evs = append(evs, journal.Event{Kind: journal.KindRunEnd, RunID: id, Step: "fix", Status: endStatus, Reason: "seed"})
	}
	for _, e := range evs {
		if _, err := lg.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runStatusOf(t *testing.T, root, id string) string {
	t.Helper()
	ref, err := journal.FindRun(root, id)
	if err != nil {
		t.Fatal(err)
	}
	return ref.State.Status()
}

func TestAbandon_FallbackWorkingCopyDeleted(t *testing.T) {
	rootA := enforcedWC(t)
	if err := journal.WriteHeartbeat(rootA, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	runID := startRun(t, rootA)
	live, _ := journal.Live(rootA)
	dirA := live[0].Dir
	lg, err := journal.OpenLog(dirA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(journal.Event{Kind: journal.KindRunEnd, RunID: runID, Step: "fix", Status: "blocked", Reason: "stale"}); err != nil {
		t.Fatal(err)
	}
	lg.Close()
	if err := journal.SyncLiveIndex(rootA); err != nil {
		t.Fatal(err)
	}
	slugA := journal.Slug(rootA)
	link := filepath.Join(journal.LiveIndexDir(), slugA+"__hookwf__"+runID)
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("precondition: live link missing: %v", err)
	}

	if err := os.RemoveAll(rootA); err != nil {
		t.Fatal(err)
	}
	rootB := t.TempDir()
	t.Chdir(rootB)

	code, out, errs := runPawl("abandon", "--run", runID)
	if code != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	wantNote := "pawl abandon: run " + runID + " is not in this working copy; abandoning it in " + slugA + " (workflow hookwf)\n"
	if errs != wantNote {
		t.Fatalf("stderr=%q want %q", errs, wantNote)
	}
	if !strings.Contains(out, "TERMINAL "+runID+" abandoned") {
		t.Fatalf("stdout=%q", out)
	}
	if got := runStatusOf(t, rootA, runID); got != "abandoned" {
		t.Fatalf("status=%q", got)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("live link not pruned: %v", err)
	}
}

func TestAbandon_CwdMatchWinsOverOtherWorkingCopy(t *testing.T) {
	rootA := setupWorkingCopy(t)
	rootB := t.TempDir()
	seedRun(t, rootA, "wf", "0617", "")
	seedRun(t, rootB, "wf", "0617", "")
	code, _, errs := runPawl("abandon", "--run", "0617")
	if code != 0 || errs != "" {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if got := runStatusOf(t, rootA, "0617"); got != "abandoned" {
		t.Fatalf("cwd run status=%q", got)
	}
	if got := runStatusOf(t, rootB, "0617"); got != "running" {
		t.Fatalf("other run status=%q", got)
	}
}

func TestAbandon_CwdTerminalBeatsOtherLive(t *testing.T) {
	rootA := setupWorkingCopy(t)
	rootB := t.TempDir()
	seedRun(t, rootA, "wf", "0617", "ok")
	seedRun(t, rootB, "wf", "0617", "")
	code, _, errs := runPawl("abandon", "--run", "0617")
	if code != 4 || !strings.Contains(errs, "has already ended (ok); nothing to abandon") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if got := runStatusOf(t, rootB, "0617"); got != "running" {
		t.Fatalf("other run status=%q", got)
	}
}

func TestAbandon_SeveralLiveElsewhereRefuses(t *testing.T) {
	setupWorkingCopy(t)
	rootB, rootC := t.TempDir(), t.TempDir()
	seedRun(t, rootB, "wf", "0617", "blocked")
	seedRun(t, rootC, "other", "0617", "")
	code, out, errs := runPawl("abandon", "--run", "0617")
	cands := []string{
		journal.Slug(rootB) + "/wf/0617 (blocked)",
		journal.Slug(rootC) + "/other/0617 (running)",
	}
	if cands[0] > cands[1] {
		cands[0], cands[1] = cands[1], cands[0]
	}
	want := `pawl abandon: run id "0617" matches live runs in several other working copies; abandon nothing: ` + strings.Join(cands, ", ") + "\n"
	if code != 1 || out != "" || errs != want {
		t.Fatalf("code=%d out=%q err=%q want %q", code, out, errs, want)
	}
	if runStatusOf(t, rootB, "0617") != "blocked" || runStatusOf(t, rootC, "0617") != "running" {
		t.Fatal("a run was abandoned")
	}
}

func TestAbandon_OnlyTerminalElsewhere(t *testing.T) {
	setupWorkingCopy(t)
	rootB := t.TempDir()
	seedRun(t, rootB, "wf", "0617", "ok")
	code, out, errs := runPawl("abandon", "--run", "0617")
	want := `pawl abandon: no run "0617" for this working copy, and no live run "0617" in any other working copy` + "\n"
	if code != 1 || out != "" || errs != want {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestAbandon_NoRunAnywhere(t *testing.T) {
	setupWorkingCopy(t)
	code, _, errs := runPawl("abandon", "--run", "0617")
	if code != 1 || !strings.Contains(errs, `no live run "0617" in any other working copy`) {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func driveFallback(t *testing.T, status string, hbAge time.Duration, withHeartbeat bool) (code int, errs, runStatus string, rootB string) {
	t.Helper()
	setupWorkingCopy(t)
	rootB = t.TempDir()
	dir := seedRun(t, rootB, "wf", "0617", status)
	if err := journal.WriteDriver(dir, "sess-x", time.Now()); err != nil {
		t.Fatal(err)
	}
	if withHeartbeat {
		if err := journal.WriteSessionHeartbeat("sess-x", time.Now().Add(-hbAge)); err != nil {
			t.Fatal(err)
		}
	}
	code, _, errs = runPawl("abandon", "--run", "0617")
	return code, errs, runStatusOf(t, rootB, "0617"), rootB
}

func TestAbandon_FallbackRefusesActivelyDrivenRun(t *testing.T) {
	code, errs, st, rootB := driveFallback(t, "", 0, true)
	want := "pawl abandon: run 0617 in " + journal.Slug(rootB) + " (workflow wf) is being driven by an active session; run pawl abandon from its working copy\n"
	if code != 4 || errs != want || st != "running" {
		t.Fatalf("code=%d err=%q status=%q", code, errs, st)
	}
}

func TestAbandon_FallbackStaleHeartbeatAllowed(t *testing.T) {
	code, errs, st, _ := driveFallback(t, "", journal.HeartbeatTTL+time.Minute, true)
	if code != 0 || st != "abandoned" {
		t.Fatalf("code=%d err=%q status=%q", code, errs, st)
	}
}

func TestAbandon_FallbackNoDriverAllowed(t *testing.T) {
	setupWorkingCopy(t)
	rootB := t.TempDir()
	seedRun(t, rootB, "wf", "0617", "")
	if code, _, errs := runPawl("abandon", "--run", "0617"); code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if runStatusOf(t, rootB, "0617") != "abandoned" {
		t.Fatal("not abandoned")
	}
}

func TestAbandon_FallbackBlockedAllowedDespiteFreshHeartbeat(t *testing.T) {
	code, errs, st, _ := driveFallback(t, "blocked", 0, true)
	if code != 0 || st != "abandoned" {
		t.Fatalf("code=%d err=%q status=%q", code, errs, st)
	}
}

func TestAbandon_FallbackRefusesLiveLockHolder(t *testing.T) {
	setupWorkingCopy(t)
	rootB := t.TempDir()
	dir := seedRun(t, rootB, "wf", "0617", "")
	lock, err := journal.AcquireLock(dir, false) // held by this (live) process
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	code, _, errs := runPawl("abandon", "--run", "0617")
	if code != 4 || !strings.Contains(errs, "is being driven by an active session") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if runStatusOf(t, rootB, "0617") != "running" {
		t.Fatal("abandoned")
	}
}
