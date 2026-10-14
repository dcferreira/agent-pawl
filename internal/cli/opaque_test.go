package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// seedOpaque writes a raw events.jsonl (and optionally plan.json) for a run of
// workflow wf under root's slug, bypassing every writer: the point is to fake
// a journal this build cannot replay.
func seedOpaque(t *testing.T, root, wf, id, events, plan string) string {
	t.Helper()
	resolved, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := journal.CreateRunDir(resolved, wf, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	if plan != "" {
		if err := os.WriteFile(filepath.Join(dir, "plan.json"), []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const newerEvents = `{"kind":"RUN_START","run_id":"ab99","seq":0,"time":"2026-01-01T00:00:00Z","format":99}` + "\n" +
	`{"kind":"FUTURE_THING","run_id":"ab99","seq":1,"time":"2026-01-01T00:00:01Z"}` + "\n"

const newerPlan = `{"format":99,"workflow":{}}`

func wantNewer(cmd, id string) string {
	return fmt.Sprintf("%s: run %s was written by a newer pawl (format 99; this pawl reads formats 1-%d); upgrade, or `pawl abandon --run %s`\n", cmd, id, journal.Format, id)
}

func TestOpaque_RunRefusesNewerFormat(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 4 {
		t.Fatalf("exit = %d, want 4; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if want := wantNewer("pawl run", "ab99"); stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if strings.Contains(stdout, "DISPATCH") {
		t.Errorf("must not start a fresh run: %q", stdout)
	}
}

func TestOpaque_RunExplicitRunRefuses(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	_, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "--run", "ab99"})
	if code != 4 || stderr != wantNewer("pawl run", "ab99") {
		t.Errorf("exit=%d stderr=%q", code, stderr)
	}
}

func TestOpaque_SubmitAndPollRefuseNewerFormat(t *testing.T) {
	root := setupWorkingCopy(t)
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", "ab99", "--step", "greet", "--json", "{}"})
	if code != 4 || stderr != wantNewer("pawl submit", "ab99") {
		t.Errorf("submit: exit=%d stderr=%q", code, stderr)
	}
	_, stderr, code = runCLI(t, []string{"pawl", "poll", "--run", "ab99", "--step", "w"})
	if code != 4 || stderr != wantNewer("pawl poll", "ab99") {
		t.Errorf("poll: exit=%d stderr=%q", code, stderr)
	}
}

func TestOpaque_CorruptSubmitExits5(t *testing.T) {
	root := setupWorkingCopy(t)
	events := `{"kind":"RUN_START","run_id":"cd01","seq":0,"time":"2026-01-01T00:00:00Z","format":2}` + "\n" +
		"garbage\n" +
		`{"kind":"STEP_ENTER","run_id":"cd01","seq":2,"step":"greet","attempt":1}` + "\n"
	dir := seedOpaque(t, root, "sample", "cd01", events, "")

	_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", "cd01", "--step", "greet", "--json", "{}"})
	if code != 5 {
		t.Fatalf("exit = %d, want 5; stderr=%q", code, stderr)
	}
	_, rerr := journal.ReadEvents(dir)
	if rerr == nil {
		t.Fatal("fixture should be unreadable")
	}
	want := fmt.Sprintf("pawl submit: run cd01 cannot be replayed (format 2): %s; repair its events.jsonl, or `pawl abandon --run cd01`\n", rerr.Error())
	if stderr != want {
		t.Errorf("stderr = %q\nwant     %q", stderr, want)
	}
}

func TestOpaque_StatusRunShowsAndExits0(t *testing.T) {
	root := setupWorkingCopy(t)
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	stdout, stderr, code := runCLI(t, []string{"pawl", "status", "--run", "ab99"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "ab99") || !strings.Contains(stdout, "newer-format") ||
		!strings.Contains(stdout, strings.TrimSuffix(strings.TrimPrefix(wantNewer("", "ab99"), ": "), "\n")) {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestOpaque_StatusListsOpaqueRuns(t *testing.T) {
	root := setupWorkingCopy(t)
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	// Single opaque live run: shown, exit 0.
	stdout, stderr, code := runCLI(t, []string{"pawl", "status"})
	if code != 0 || !strings.Contains(stdout, "newer-format") || !strings.Contains(stdout, "ab99") {
		t.Fatalf("single: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	// A second live run: multi-run error lists both with safe status text.
	seedRun(t, root, "other", "ee11", "")
	_, stderr, code = runCLI(t, []string{"pawl", "status"})
	if code != 1 || !strings.Contains(stderr, "ab99") || !strings.Contains(stderr, "ee11") {
		t.Errorf("multi: exit=%d stderr=%q", code, stderr)
	}
}

func TestOpaque_AbandonThenRunStartsFresh(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	dir := seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	stdout, stderr, code := runCLI(t, []string{"pawl", "abandon", "--run", "ab99"})
	if code != 0 {
		t.Fatalf("abandon exit = %d stdout=%q stderr=%q", code, stdout, stderr)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if !strings.Contains(string(data), `"kind":"RUN_END"`) || !strings.Contains(string(data), `"seq":2`) {
		t.Errorf("events.jsonl lacks RUN_END seq 2: %s", data)
	}

	// Again: already ended.
	_, stderr, code = runCLI(t, []string{"pawl", "abandon", "--run", "ab99"})
	if code != 4 || !strings.Contains(stderr, "has already ended") {
		t.Errorf("second abandon: exit=%d stderr=%q", code, stderr)
	}

	stdout, stderr, code = runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 || !strings.Contains(stdout, "DISPATCH") {
		t.Errorf("run after abandon: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestOpaque_FreshStartsNewRun(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "--fresh", "name=Ada"})
	if code != 0 || !strings.Contains(stdout, "DISPATCH") {
		t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestOpaque_RunMultipleCandidates(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)
	seedRun(t, root, "sample", "ee11", "")

	_, stderr, code := runCLI(t, []string{"pawl", "run", "sample"})
	if code != 1 || !strings.Contains(stderr, "multiple runs are live") || !strings.Contains(stderr, "ab99") {
		t.Errorf("exit=%d stderr=%q", code, stderr)
	}
}

// TestCompat_Format1RunResumes proves a real format-1 journal (an in-progress
// wait-for-build run parked at wait_for_build) still resumes under this build
// with no digest mismatch, re-printing its WAIT block.
func TestCompat_Format1RunResumes(t *testing.T) {
	root := setupWorkingCopy(t)
	_, thisFile, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(thisFile), "..", "journal", "testdata", "compat", "format1")
	wf, err := os.ReadFile(filepath.Join(fixture, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeWorkflow(t, root, "wait-for-build", string(wf))
	wfPath := filepath.Join(root, ".claude", "workflows", "wait-for-build.yaml")

	resolved, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := journal.CreateRunDir(resolved, "wait-for-build", "064a")
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := os.ReadFile(filepath.Join(fixture, "events.jsonl"))
	plan, _ := os.ReadFile(filepath.Join(fixture, "plan.json"))
	if !strings.Contains(string(plan), `"Path": "/redacted/workflow.yaml"`) {
		t.Fatal("fixture plan.json Path changed")
	}
	plan = []byte(strings.Replace(string(plan), `"Path": "/redacted/workflow.yaml"`, fmt.Sprintf(`"Path": %q`, wfPath), 1))
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), ev, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), plan, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "wait-for-build"})
	if code != 0 {
		t.Fatalf("exit = %d; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "WAIT 064a wait_for_build") {
		t.Errorf("stdout lacks WAIT block for wait_for_build: %q", stdout)
	}
	if strings.Contains(stderr, "changed") {
		t.Errorf("unexpected digest complaint: %q", stderr)
	}
}

// A replayable journal whose plan.json is newer: the engine/pinned-workflow
// path hits *journal.FormatError and must surface the same (a) text, exit 4.
func TestOpaque_NewerPlanOnEnginePathExits4(t *testing.T) {
	root := setupWorkingCopy(t)
	resolved, _ := journal.ResolveRoot(root)
	dir := seedRun(t, resolved, "sample", "ee22", "")
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), []byte(newerPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", "ee22", "--step", "fix", "--json", "{}"})
	if code != 4 || stderr != wantNewer("pawl submit", "ee22") {
		t.Errorf("exit=%d stderr=%q", code, stderr)
	}
}

func TestOpaque_TypeMismatchedNewerFormatExits4(t *testing.T) {
	root := setupWorkingCopy(t)
	seedOpaque(t, root, "sample", "ab99", newerEvents+`{"kind":"FUTURE_THING","run_id":"ab99","seq":"x"}`+"\n", newerPlan)
	_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", "ab99", "--step", "s", "--json", "{}"})
	if code != 4 || stderr != wantNewer("pawl submit", "ab99") {
		t.Errorf("exit=%d stderr=%q", code, stderr)
	}
}

func TestOpaque_StatusAndSubmitAfterAbandon(t *testing.T) {
	root := setupWorkingCopy(t)
	seedOpaque(t, root, "sample", "ab99", newerEvents, newerPlan)
	if _, stderr, code := runCLI(t, []string{"pawl", "abandon", "--run", "ab99"}); code != 0 {
		t.Fatalf("abandon exit=%d stderr=%q", code, stderr)
	}
	stdout, stderr, code := runCLI(t, []string{"pawl", "status", "--run", "ab99"})
	if code != 0 || !strings.Contains(stdout, "abandoned") || strings.Contains(stdout, "newer-format") || strings.Contains(stdout, "upgrade") {
		t.Errorf("status: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, stderr, code = runCLI(t, []string{"pawl", "submit", "--run", "ab99", "--step", "s", "--json", "{}"})
	if code != 4 || !strings.Contains(stderr, "has already ended") {
		t.Errorf("submit: exit=%d stderr=%q", code, stderr)
	}
}

func TestOpaque_StatusJSONEnvelope(t *testing.T) {
	root := setupWorkingCopy(t)
	const wfPath = "/elsewhere/review.yaml"
	seedOpaque(t, root, "sample", "ab99", newerEvents, `{"format":99,"future":1,"workflow":{"path":"`+wfPath+`"}}`)
	// A same-named decoy that id-resolution would wrongly report.
	decoy := filepath.Join(root, ".claude", "workflows", "sample.yaml")
	if err := os.MkdirAll(filepath.Dir(decoy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLI(t, []string{"pawl", "status", "--run", "ab99", "--json"})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	var got struct {
		SchemaVersion int `json:"schema_version"`
		Root          string
		Runs          []struct {
			RunID        string `json:"run_id"`
			Workflow     string
			WorkflowPath string `json:"workflow_path"`
			Status       string
			Reason       string
		}
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got.Runs) != 1 {
		t.Fatalf("stdout=%q err=%v", stdout, err)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", got.SchemaVersion)
	}
	if got.Runs[0].Workflow != "sample" {
		t.Errorf("workflow = %q, want the workflow-id dir name %q", got.Runs[0].Workflow, "sample")
	}
	if got.Runs[0].WorkflowPath != wfPath {
		t.Errorf("workflow_path = %q, want %q", got.Runs[0].WorkflowPath, wfPath)
	}
	if r := got.Runs[0]; r.RunID != "ab99" || r.Status != "newer-format" || !strings.Contains(r.Reason, "newer pawl") {
		t.Errorf("run = %+v", r)
	}
}
