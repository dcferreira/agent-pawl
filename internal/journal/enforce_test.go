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
	if !hb.Fresh(now.Add(10 * time.Second)) {
		t.Fatal("10s-old heartbeat should be fresh")
	}
	if hb.Fresh(now.Add(10*time.Second + time.Nanosecond)) {
		t.Fatal(">10s-old heartbeat should be stale")
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
