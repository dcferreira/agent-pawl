package journal

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeEventsFile(t *testing.T, dir string, events []Event) {
	t.Helper()
	l, err := OpenLog(dir)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer l.Close()
	for _, e := range events {
		if _, err := l.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func TestLive_FiltersTerminalRuns(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/home/user/proj"

	doneDir, err := CreateRunDir(root, "ship", "r-done")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, doneDir, completedRunFixture())

	blockedDir, err := CreateRunDir(root, "ship", "r-blocked")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, blockedDir, blockedRunFixture())

	runningDir, err := CreateRunDir(root, "other", "r-running")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, runningDir, []Event{{Kind: KindRunStart, RunID: "r-running"}, {Kind: KindStepEnter, RunID: "r-running", Step: "s1", Attempt: 1}})

	live, err := Live(root)
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	var ids []string
	for _, r := range live {
		ids = append(ids, r.RunID)
	}
	sort.Strings(ids)
	want := []string{"r-blocked", "r-running"}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Errorf("Live() run ids = %v, want %v (r-done is terminal)", ids, want)
	}
}

func TestLive_NoRunsForRoot(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	live, err := Live("/nope")
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(live) != 0 {
		t.Errorf("Live() = %v, want empty", live)
	}
}

// TestLive_SkipsNeverStartedRun is half of I4: a run directory that exists
// (e.g. created by CreateRunDir just before a crash, before RUN_START ever
// landed) but has no events.jsonl at all must not count as live — it has
// nothing to resume, and counting it would silently break `wf run`'s
// resume-when-exactly-one rule.
func TestLive_SkipsNeverStartedRun(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/home/user/proj"

	if _, err := CreateRunDir(root, "ship", "r-empty"); err != nil {
		t.Fatal(err)
	}

	live, err := Live(root)
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(live) != 0 {
		t.Errorf("Live() = %+v, want empty (never-started run must not count as live)", live)
	}
}

// TestLive_SkipsCorruptRunRatherThanAbortingTheScan is I4's other half: one
// unreadable/corrupt run directory must not hide every other live run for
// the working copy from `wf run`'s resume rule.
func TestLive_SkipsCorruptRunRatherThanAbortingTheScan(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/home/user/proj"

	corruptDir, err := CreateRunDir(root, "ship", "r-corrupt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "events.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runningDir, err := CreateRunDir(root, "ship", "r-running")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, runningDir, []Event{{Kind: KindRunStart, RunID: "r-running"}})

	live, err := Live(root)
	if err != nil {
		t.Fatalf("Live: want the scan to continue past the corrupt run, got error: %v", err)
	}
	if len(live) != 1 || live[0].RunID != "r-running" {
		t.Errorf("Live() = %+v, want just r-running (r-corrupt skipped)", live)
	}
}

func TestLive_UsesResolvedRootSlug(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/a/b"
	dir := RunDir(root, "wf1", "r1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, dir, []Event{{Kind: KindRunStart, RunID: "r1"}})

	live, err := Live(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].Dir != filepath.Clean(dir) {
		t.Errorf("Live() = %+v, want one run at %s", live, dir)
	}
}
