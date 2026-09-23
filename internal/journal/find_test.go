package journal

import (
	"strings"
	"testing"
)

// TestFindRun_FindsTerminalRun is the difference from Live(): FindRun must
// locate a run by id whether or not it is terminal, since pawl status --run
// <id> and pawl abandon --run <id> both need to look up a specific run
// (including one abandon has already ended) rather than enumerate every
// live one.
func TestFindRun_FindsTerminalRun(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/home/user/proj"

	doneDir, err := CreateRunDir(root, "ship", "r-done")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, doneDir, completedRunFixture())

	ref, err := FindRun(root, "r-done")
	if err != nil {
		t.Fatalf("FindRun: %v", err)
	}
	if ref.RunID != "r-done" || ref.WorkflowID != "ship" {
		t.Errorf("FindRun = %+v, want RunID r-done, WorkflowID ship", ref)
	}
	if !ref.State.Terminal() {
		t.Errorf("FindRun's state should be terminal (r1 ended ok)")
	}
	if ref.State.EndStatus != "ok" {
		t.Errorf("EndStatus = %q, want ok", ref.State.EndStatus)
	}
}

func TestFindRun_FindsLiveRun(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/home/user/proj"

	runningDir, err := CreateRunDir(root, "other", "r-running")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, runningDir, []Event{{Kind: KindRunStart, RunID: "r-running"}, {Kind: KindStepEnter, RunID: "r-running", Step: "s1", Attempt: 1}})

	ref, err := FindRun(root, "r-running")
	if err != nil {
		t.Fatalf("FindRun: %v", err)
	}
	if ref.RunID != "r-running" {
		t.Errorf("FindRun = %+v, want RunID r-running", ref)
	}
}

func TestFindRun_NoSuchRun(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/home/user/proj"

	if _, err := FindRun(root, "nope"); err == nil {
		t.Errorf("FindRun: expected an error for a run id that does not exist")
	}
}

// TestFindRun_PrefersLiveOverTerminalCollision is reviewer finding B4: run
// ids are only 4 hex characters (journal/rundir.go), so a live run in one
// workflow can collide with an old, ended run of the same id in another
// workflow — os.ReadDir's order would otherwise decide which one FindRun
// returns, silently. When exactly one of the colliding candidates is still
// live, FindRun must prefer it: that's overwhelmingly the one a caller means
// (a fresh `pawl status`/`pawl abandon --run <id>` is about what's running
// now, not archaeology), and it is the only one abandon could act on anyway.
func TestFindRun_PrefersLiveOverTerminalCollision(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/home/user/proj"

	oldDir, err := CreateRunDir(root, "ship", "abcd")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, oldDir, completedRunFixture())

	newDir, err := CreateRunDir(root, "other", "abcd")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, newDir, []Event{{Kind: KindRunStart, RunID: "abcd"}, {Kind: KindStepEnter, RunID: "abcd", Step: "s1", Attempt: 1}})

	ref, err := FindRun(root, "abcd")
	if err != nil {
		t.Fatalf("FindRun: %v", err)
	}
	if ref.WorkflowID != "other" {
		t.Errorf("FindRun picked WorkflowID %q, want the live one (other), not the terminal one (ship)", ref.WorkflowID)
	}
	if ref.State.Terminal() {
		t.Errorf("FindRun picked a terminal run over a live one")
	}
}

// TestFindRun_AmbiguousCollision covers the case TestFindRun_
// PrefersLiveOverTerminalCollision does not resolve: two (or more) live runs
// across different workflows sharing the same 4-hex-character id. There is
// no principled way to prefer one, so FindRun must refuse and name every
// candidate (workflow + status) rather than silently picking whichever
// os.ReadDir happened to list first.
func TestFindRun_AmbiguousCollision(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	root := "/home/user/proj"

	dir1, err := CreateRunDir(root, "ship", "abcd")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, dir1, []Event{{Kind: KindRunStart, RunID: "abcd"}, {Kind: KindStepEnter, RunID: "abcd", Step: "s1", Attempt: 1}})

	dir2, err := CreateRunDir(root, "other", "abcd")
	if err != nil {
		t.Fatal(err)
	}
	writeEventsFile(t, dir2, []Event{{Kind: KindRunStart, RunID: "abcd"}, {Kind: KindStepEnter, RunID: "abcd", Step: "s1", Attempt: 1}})

	_, err = FindRun(root, "abcd")
	if err == nil {
		t.Fatal("FindRun: expected an ambiguity error for two live runs sharing an id, got nil")
	}
	if !strings.Contains(err.Error(), "ship") || !strings.Contains(err.Error(), "other") {
		t.Errorf("FindRun error = %q, want it to name both candidate workflows (ship, other)", err.Error())
	}
}
