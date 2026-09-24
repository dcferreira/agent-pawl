package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withStateDir(t *testing.T) string {
	t.Helper()
	base := filepath.Join(t.TempDir(), "runs")
	t.Setenv(EnvStateDir, base)
	return base
}

func TestPawlHome_IsParentOfStateBase(t *testing.T) {
	base := withStateDir(t)
	if got, want := PawlHome(), filepath.Dir(base); got != want {
		t.Fatalf("PawlHome() = %q, want %q", got, want)
	}
	if got, want := LiveIndexDir(), filepath.Join(filepath.Dir(base), "live"); got != want {
		t.Fatalf("LiveIndexDir() = %q, want %q", got, want)
	}
}

func TestHeartbeat_RoundTripAndFreshness(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if _, ok, err := ReadHeartbeat(root); err != nil || ok {
		t.Fatalf("absent heartbeat: ok=%v err=%v", ok, err)
	}
	if err := WriteHeartbeat(root, "sess-1", now); err != nil {
		t.Fatal(err)
	}
	hb, ok, err := ReadHeartbeat(root)
	if err != nil || !ok || hb.SessionID != "sess-1" || !hb.Time.Equal(now) {
		t.Fatalf("got %+v ok=%v err=%v", hb, ok, err)
	}
	if !hb.Fresh(now.Add(5 * time.Minute)) {
		t.Fatal("5m-old heartbeat should be fresh")
	}
	if hb.Fresh(now.Add(5*time.Minute + time.Nanosecond)) {
		t.Fatal(">5m-old heartbeat should be stale")
	}
	if hb.Fresh(now.Add(-time.Second)) {
		t.Fatal("heartbeat from the future should not count as fresh")
	}
}

func TestHeartbeat_SeparatePerWorkingCopy(t *testing.T) {
	withStateDir(t)
	a, b := t.TempDir(), t.TempDir()
	now := time.Now()
	if err := WriteHeartbeat(a, "sess-a", now); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadHeartbeat(b); ok {
		t.Fatal("heartbeat for a leaked into b")
	}
}

func TestDriver_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ReadDriver(dir); err != nil || ok {
		t.Fatalf("absent driver: ok=%v err=%v", ok, err)
	}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if err := WriteDriver(dir, "sess-9", now); err != nil {
		t.Fatal(err)
	}
	d, ok, err := ReadDriver(dir)
	if err != nil || !ok || d.SessionID != "sess-9" || !d.Updated.Equal(now) {
		t.Fatalf("got %+v ok=%v err=%v", d, ok, err)
	}
}

// startRun makes a minimal live run directory for root (one RUN_START
// event), using the package's own Log so the fixture matches real runs.
// Log.Append takes and returns an Event by value (see log.go), not a
// pointer.
func startRun(t *testing.T, root, workflowID, runID string) string {
	t.Helper()
	dir, err := CreateRunDir(root, workflowID, runID)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if _, err := lg.Append(Event{RunID: runID, Kind: KindRunStart}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// endRun appends a terminal RUN_END event (status "ok") to an already-live
// run directory, so a caller can assert that SyncLiveIndex gives a
// now-terminal run no link.
func endRun(t *testing.T, dir, runID string) {
	t.Helper()
	lg, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if _, err := lg.Append(Event{RunID: runID, Kind: KindRunEnd, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncLiveIndex_CreatesLinkForLiveRun(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	dir := startRun(t, root, "wf", "ab12")
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(LiveIndexDir(), Slug(root)+"__wf__ab12")
	target, err := os.Readlink(link)
	if err != nil || target != dir {
		t.Fatalf("link %s -> %q (err %v), want %q", link, target, err, dir)
	}
}

func TestSyncLiveIndex_RemovesStale(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	dir := startRun(t, root, "wf", "ab12")
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(LiveIndexDir())
	if len(entries) != 0 {
		t.Fatalf("stale link survived: %v", entries)
	}
}

// TestSyncLiveIndex_NoLinkForTerminalRun covers the terminal-run case: once
// a run has ended (a RUN_END with a non-"blocked" status), Live(root) no
// longer includes it, so SyncLiveIndex must not leave (or create) a link
// for it, even though its run directory still exists on disk.
func TestSyncLiveIndex_NoLinkForTerminalRun(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	dir := startRun(t, root, "wf", "ab12")
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(LiveIndexDir(), Slug(root)+"__wf__ab12")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("expected a link for the live run before it ends: %v", err)
	}

	endRun(t, dir, "ab12")
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("terminal run kept its live-index link: err=%v", err)
	}
}

func TestSyncLiveIndex_LeavesOtherWorkingCopiesAlone(t *testing.T) {
	withStateDir(t)
	a, b := t.TempDir(), t.TempDir()
	startRun(t, a, "wf", "aaaa")
	if err := SyncLiveIndex(a); err != nil {
		t.Fatal(err)
	}
	if err := SyncLiveIndex(b); err != nil { // b has no runs
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(LiveIndexDir(), Slug(a)+"__wf__aaaa")); err != nil {
		t.Fatalf("syncing b removed a's link: %v", err)
	}
}

// A dangling live/ symlink (its run directory deleted, e.g. with its whole
// checkout) is pruned by any working copy's sync, whatever its slug: no
// other sync would ever reach it, and it keeps bin/pawl-hook's fast path
// thinking a run is live.
func TestSyncLiveIndex_PrunesDanglingLinksOfAnySlug(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	if err := os.MkdirAll(LiveIndexDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(LiveIndexDir(), "-gone-checkout-1234__wf__dead")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	startRun(t, other, "wf", "aaaa")
	if err := SyncLiveIndex(other); err != nil {
		t.Fatal(err)
	}
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dangling); !os.IsNotExist(err) {
		t.Fatalf("dangling link survived: err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(LiveIndexDir(), Slug(other)+"__wf__aaaa")); err != nil {
		t.Fatalf("a live link of another slug must survive: %v", err)
	}
}

// A run started with enforcement opted out is invisible to the hooks, so
// it gets no live/ link to wake bin/pawl-hook's fast path for nothing.
func TestSyncLiveIndex_SkipsEnforcementOffRun(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	dir, err := CreateRunDir(root, "wf", "ab12")
	if err != nil {
		t.Fatal(err)
	}
	lg, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(Event{RunID: "ab12", Kind: KindRunStart, HookSelfTest: "off (--no-enforcement)"}); err != nil {
		t.Fatal(err)
	}
	lg.Close()
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(LiveIndexDir(), Slug(root)+"__wf__ab12")); !os.IsNotExist(err) {
		t.Fatalf("opted-out run got a live link: err=%v", err)
	}
}

// LiveIndexed finds live runs through live/ across every working copy,
// verifying each link against the journal: a terminal run, a link whose
// target is outside the state base, and a link whose name does not match
// its target are all skipped.
func TestLiveIndexed_AcrossWorkingCopies(t *testing.T) {
	withStateDir(t)
	a, b := t.TempDir(), t.TempDir()
	startRun(t, a, "wf", "aaaa")
	dirB := startRun(t, b, "wf", "bbbb")
	ended := startRun(t, b, "wf", "cccc")
	for _, r := range []string{a, b} {
		if err := SyncLiveIndex(r); err != nil {
			t.Fatal(err)
		}
	}
	// A link that outlived its run's end (synced while it was live).
	endRun(t, ended, "cccc")
	// A link to a run directory outside the state base.
	outside := startRunAt(t, filepath.Join(t.TempDir(), "x", "wf", "dddd"), "dddd")
	if err := os.Symlink(outside, filepath.Join(LiveIndexDir(), "x__wf__dddd")); err != nil {
		t.Fatal(err)
	}
	// A link whose name disagrees with its target.
	if err := os.Symlink(dirB, filepath.Join(LiveIndexDir(), Slug(a)+"__wf__eeee")); err != nil {
		t.Fatal(err)
	}
	refs, err := LiveIndexed()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range refs {
		got[r.RunID] = true
	}
	if len(refs) != 2 || !got["aaaa"] || !got["bbbb"] {
		t.Fatalf("LiveIndexed = %+v, want exactly aaaa and bbbb", refs)
	}
}

func startRunAt(t *testing.T, dir, runID string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lg, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if _, err := lg.Append(Event{RunID: runID, Kind: KindRunStart}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A trailing slash on PAWL_STATE_DIR must not make PawlHome the state dir
// itself (filepath.Dir("/x/runs/") is "/x/runs"); bin/pawl-hook strips it
// the same way.
func TestPawlHome_TrailingSlashInStateDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runs")
	t.Setenv(EnvStateDir, base+"//")
	if got, want := PawlHome(), filepath.Dir(base); got != want {
		t.Fatalf("PawlHome() = %q, want %q", got, want)
	}
}
