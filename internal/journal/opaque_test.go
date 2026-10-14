package journal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRaw(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, eventsFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const future99 = `{"kind":"RUN_START","run_id":"r99","seq":0,"time":"2026-01-01T00:00:00Z","format":99}` + "\n" +
	`{"kind":"FUTURE_THING","run_id":"r99","seq":1,"time":"2026-01-01T00:00:01Z"}` + "\n"

func TestLive_NewerFormatRunStaysVisible(t *testing.T) {
	t.Setenv(EnvStateDir, t.TempDir())
	root := "/home/user/proj"
	dir, err := CreateRunDir(root, "wf", "r99")
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, dir, future99)

	live, err := Live(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Fatalf("Live() = %d runs, want 1", len(live))
	}
	r := live[0]
	if r.State != nil || r.Opaque == nil {
		t.Fatalf("State=%v Opaque=%v, want nil State and set Opaque", r.State, r.Opaque)
	}
	if !r.Opaque.Newer() || r.Opaque.Format != 99 || !errors.Is(r.Opaque.Err, ErrNewerFormat) {
		t.Errorf("opaque = %+v, want newer format 99", r.Opaque)
	}
	if r.Opaque.RunID != "r99" || r.Opaque.LastSeq != 1 || r.Opaque.Ended {
		t.Errorf("opaque scan = %+v", r.Opaque)
	}
	if r.Terminal() || r.StatusText() != "newer-format" {
		t.Errorf("Terminal=%v StatusText=%q", r.Terminal(), r.StatusText())
	}
	found, err := FindRun(root, "r99")
	if err != nil || found.Opaque == nil {
		t.Fatalf("FindRun = %+v, %v", found, err)
	}
}

func TestLive_CorruptKnownFormatRunIsUnreadable(t *testing.T) {
	t.Setenv(EnvStateDir, t.TempDir())
	root := "/home/user/proj"
	dir, _ := CreateRunDir(root, "wf", "rbad")
	writeRaw(t, dir, `{"kind":"RUN_START","run_id":"rbad","seq":0,"format":2}`+"\ngarbage\n"+
		`{"kind":"STEP_ENTER","run_id":"rbad","seq":2,"step":"s"}`+"\n")
	live, err := Live(root)
	if err != nil || len(live) != 1 {
		t.Fatalf("Live = %v, %v", live, err)
	}
	r := live[0]
	if r.Opaque == nil || r.Opaque.Newer() || r.Opaque.Err == nil || r.Opaque.Format != 2 {
		t.Fatalf("opaque = %+v", r.Opaque)
	}
	if r.StatusText() != "unreadable" || r.Opaque.LastSeq != 2 {
		t.Errorf("StatusText=%q LastSeq=%d", r.StatusText(), r.Opaque.LastSeq)
	}
}

func TestAllRuns_EmptyEventsStillSkipped(t *testing.T) {
	t.Setenv(EnvStateDir, t.TempDir())
	root := "/home/user/proj"
	dir, _ := CreateRunDir(root, "wf", "rempty")
	writeRaw(t, dir, "")
	live, err := Live(root)
	if err != nil || len(live) != 0 {
		t.Fatalf("Live = %v, %v", live, err)
	}
}

func TestScanEvents_EndedDetection(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, future99+`{"kind":"RUN_END","run_id":"r99","seq":2,"status":"blocked"}`+"\n")
	s, err := scanEvents(dir)
	if err != nil || s.Ended || s.LastSeq != 2 {
		t.Fatalf("blocked: %+v %v", s, err)
	}
	writeRaw(t, dir, future99+`{"kind":"RUN_END","run_id":"r99","seq":2,"status":"abandoned"}`+"\n")
	s, _ = scanEvents(dir)
	if !s.Ended {
		t.Errorf("abandoned RUN_END not Ended: %+v", s)
	}
	// An event after RUN_END means the run is not (last-event) ended.
	writeRaw(t, dir, future99+`{"kind":"RUN_END","run_id":"r99","seq":2,"status":"abandoned"}`+"\n"+`{"kind":"X","run_id":"r99","seq":3}`+"\n")
	s, _ = scanEvents(dir)
	if s.Ended {
		t.Errorf("Ended with later event: %+v", s)
	}
}

func TestAppendRunEndRaw_EndsOpaqueRun(t *testing.T) {
	t.Setenv(EnvStateDir, t.TempDir())
	root := "/home/user/proj"
	dir, _ := CreateRunDir(root, "wf", "r99")
	writeRaw(t, dir, future99)
	live, _ := Live(root)
	if len(live) != 1 {
		t.Fatal("setup")
	}
	if err := AppendRunEndRaw(dir, "r99", live[0].Opaque.LastSeq+1, "abandoned", "operator abandoned"); err != nil {
		t.Fatal(err)
	}
	s, err := scanEvents(dir)
	if err != nil || !s.Ended || s.LastSeq != 2 {
		t.Fatalf("scan = %+v %v", s, err)
	}
	live, _ = Live(root)
	if len(live) != 0 {
		t.Errorf("Live after abandon = %d runs, want 0", len(live))
	}
	ref, err := FindRun(root, "r99")
	if err != nil || !ref.Terminal() {
		t.Errorf("FindRun = %+v %v", ref, err)
	}
}

func TestAppendRunEndRaw_AfterTornTail(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, future99+`{"kind":"FUTURE_THING","run_id":"r99","seq":2,"ti`)
	if err := AppendRunEndRaw(dir, "r99", 2, "abandoned", "x"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, eventsFileName))
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d: %q", len(lines), data)
	}
	s, err := scanEvents(dir)
	if err != nil || !s.Ended {
		t.Fatalf("scan = %+v %v", s, err)
	}
}

func TestSyncLiveIndex_OpaqueRunKeepsLink(t *testing.T) {
	t.Setenv(EnvStateDir, t.TempDir())
	root := "/home/user/proj"
	dir, _ := CreateRunDir(root, "wf", "r99")
	writeRaw(t, dir, future99)
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	idx, err := LiveIndexed()
	if err != nil || len(idx) != 1 || idx[0].Opaque == nil {
		t.Fatalf("LiveIndexed = %+v %v", idx, err)
	}
	if err := AppendRunEndRaw(dir, "r99", 2, "abandoned", "x"); err != nil {
		t.Fatal(err)
	}
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	idx, _ = LiveIndexed()
	if len(idx) != 0 {
		t.Errorf("LiveIndexed after end = %+v", idx)
	}
}

func TestFindLiveRunElsewhere_OpaqueRun(t *testing.T) {
	t.Setenv(EnvStateDir, t.TempDir())
	dir, _ := CreateRunDir("/home/user/other", "wf", "r99")
	writeRaw(t, dir, future99)
	got, err := FindLiveRunElsewhere("r99", Slug("/home/user/proj"))
	if err != nil || len(got) != 1 || got[0].Opaque == nil {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestReadEvents_NewlineTerminatedGarbageFinalLineStillErrors(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, `{"kind":"RUN_START","run_id":"r","seq":0}`+"\n"+"garbage\n")
	if _, err := ReadEvents(dir); err == nil {
		t.Fatal("want error")
	}
	writeRaw(t, dir, `{"kind":"RUN_START","run_id":"r","seq":0}`+"\ngarbage\n"+`{"kind":"X","run_id":"r","seq":2}`+"\n")
	if _, err := ReadEvents(dir); err == nil {
		t.Fatal("want error for mid-file garbage")
	}
}

func TestLive_NewerFormatWithTypeMismatchedRecordIsNewer(t *testing.T) {
	t.Setenv(EnvStateDir, t.TempDir())
	root := "/home/user/proj"
	dir, _ := CreateRunDir(root, "wf", "r99")
	writeRaw(t, dir, future99+`{"kind":"FUTURE_THING","run_id":"r99","seq":"x","time":"2026-01-01T00:00:02Z"}`+"\n")
	live, err := Live(root)
	if err != nil || len(live) != 1 || live[0].Opaque == nil {
		t.Fatalf("Live = %+v, %v", live, err)
	}
	if !live[0].Opaque.Newer() || live[0].StatusText() != "newer-format" {
		t.Errorf("opaque = %+v, want newer", live[0].Opaque)
	}
}
