package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// clockAhead makes the cli's shared clock (nowFunc) run d ahead of the wall
// clock for the rest of the test, so a run that was just journalled reads as
// idle for about d.
func clockAhead(t *testing.T, d time.Duration) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return time.Now().Add(d) }
	t.Cleanup(func() { nowFunc = old })
}

const day = 24 * time.Hour

func TestFormatIdleAge(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{22*day + 3*time.Hour, "22d"},
		{7 * day, "7d"},
		{day, "1d"},
		{day - time.Second, "23h"},
		{36 * time.Hour, "1d"},
		{90 * time.Minute, "1h"},
		{time.Hour, "1h"},
	} {
		if got := formatIdleAge(tc.d); got != tc.want {
			t.Errorf("formatIdleAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestIdleAge(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rs, err := journal.Replay([]journal.Event{
		{Kind: journal.KindRunStart, RunID: "r1", Seq: 0, Time: t0},
		{Kind: journal.KindStepEnter, RunID: "r1", Seq: 1, Time: t0, Step: "s", Attempt: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := idleAge(rs, t0.Add(22*day+time.Hour)); got != "22d" {
		t.Errorf("stale run: idleAge = %q, want 22d", got)
	}
	if got := idleAge(rs, t0.Add(6*day)); got != "" {
		t.Errorf("run under the threshold: idleAge = %q, want empty", got)
	}
	if got := idleAge(nil, t0.Add(22*day)); got != "" {
		t.Errorf("nil state: idleAge = %q, want empty", got)
	}
}

func TestStatus_IdleLine(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	id := startStatusRun(t, "sample", "name=Ada")

	// Fresh: nothing new in the output.
	out, _, code := runCLI(t, []string{"pawl", "status"})
	if code != 0 || strings.Contains(out, "idle:") {
		t.Fatalf("fresh run: exit %d, want 0 and no idle line; out = %q", code, out)
	}

	// Just under the threshold: still nothing.
	clockAhead(t, 6*day)
	out, _, code = runCLI(t, []string{"pawl", "status"})
	if code != 0 || strings.Contains(out, "idle:") {
		t.Fatalf("6d idle: exit %d, want 0 and no idle line; out = %q", code, out)
	}

	// Past it: one extra line right after status:, exit unchanged.
	clockAhead(t, 22*day)
	out, _, code = runCLI(t, []string{"pawl", "status"})
	if code != 0 {
		t.Fatalf("22d idle: exit %d, want 0", code)
	}
	want := "idle: 22d since the last journal event; to drop it: pawl abandon --run " + id + "\n"
	if !strings.Contains(out, "status: running\n"+want) {
		t.Errorf("want %q directly after the status line, got:\n%s", want, out)
	}
	// --run takes the same path.
	out, _, _ = runCLI(t, []string{"pawl", "status", "--run", id})
	if !strings.Contains(out, want) {
		t.Errorf("--run: want %q, got:\n%s", want, out)
	}

	// A finished run is never flagged.
	if _, errOut, code := runCLI(t, []string{"pawl", "abandon", "--run", id}); code != 0 {
		t.Fatalf("abandon: exit %d %q", code, errOut)
	}
	out, _, _ = runCLI(t, []string{"pawl", "status", "--run", id})
	if strings.Contains(out, "idle:") {
		t.Errorf("abandoned run must not carry an idle line; got:\n%s", out)
	}
}

func TestStatus_IdleMultiRun(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	a := startStatusRun(t, "sample", "name=Ada")
	b := startStatusRun(t, "sample", "--fresh", "name=Bob")

	_, errOut, code := runCLI(t, []string{"pawl", "status"})
	if code != 1 || strings.Contains(errOut, "idle") {
		t.Fatalf("fresh: exit %d (want 1), stderr must not mention idle: %q", code, errOut)
	}

	clockAhead(t, 22*day)
	_, errOut, code = runCLI(t, []string{"pawl", "status"})
	if code != 1 {
		t.Fatalf("exit %d, want 1 unchanged", code)
	}
	for _, id := range []string{a, b} {
		if !strings.Contains(errOut, id+" (idle 22d)") {
			t.Errorf("stderr lacks %q:\n%s", id+" (idle 22d)", errOut)
		}
	}
}

func TestStatus_IdleMultiRunComposesWithOpaque(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	a := startStatusRun(t, "sample", "name=Ada")
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	clockAhead(t, 22*day)
	_, errOut, code := runCLI(t, []string{"pawl", "status"})
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, a+" (idle 22d)") {
		t.Errorf("stderr lacks the idle suffix for %s:\n%s", a, errOut)
	}
	if !strings.Contains(errOut, "ab99 (") || strings.Contains(errOut, "ab99 (idle") {
		t.Errorf("the opaque run keeps only its own status suffix:\n%s", errOut)
	}
}

func TestStatusJSON_LastEventTime(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	startStatusRun(t, "sample", "name=Ada")
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	before := time.Now().UTC().Add(-time.Minute)
	out, _, code := runCLI(t, []string{"pawl", "status", "--json"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, r := range statusRuns(t, out) {
		v, ok := r["last_event_time"].(string)
		if !ok {
			t.Fatalf("run %v: last_event_time missing or not a string: %#v", r["run_id"], r["last_event_time"])
		}
		if r["run_id"] == "ab99" {
			if v != "" {
				t.Errorf("opaque run: last_event_time = %q, want empty", v)
			}
			continue
		}
		if !strings.HasSuffix(v, "Z") {
			t.Errorf("last_event_time %q is not UTC (RFC3339 with Z)", v)
		}
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatalf("last_event_time %q: %v", v, err)
		}
		if ts.Before(before) || ts.After(time.Now().Add(time.Minute)) {
			t.Errorf("last_event_time %v is not about now", ts)
		}
	}
	// No policy keys are frozen alongside it.
	for _, r := range statusRuns(t, out) {
		for _, k := range []string{"stale", "idle_seconds"} {
			if _, has := r[k]; has {
				t.Errorf("run %v carries %q; only last_event_time is added", r["run_id"], k)
			}
		}
	}
}
