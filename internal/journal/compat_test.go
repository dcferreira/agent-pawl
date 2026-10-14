package journal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

// testdata/compat/format1 is a real journal written by a format-1 pawl (no
// format stamps; plan.json serialised with Go field names), redacted:
// events.jsonl and plan.json keep kinds, seq, step ids, attempts, outcome
// names and the graph, with every value, command, description and path
// replaced by a placeholder. workflow.yaml is the equivalent authored file.
// Format-1 journals must stay readable by every later build.
const compatDir = "testdata/compat/format1"

func TestCompatFormat1_EventsReplay(t *testing.T) {
	events, err := ReadEvents(compatDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Fatalf("events = %d, want 6", len(events))
	}
	if events[0].Format != 0 {
		t.Errorf("format-1 RUN_START carries a stamp: %d", events[0].Format)
	}
	rs, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if rs.Cursor.Step != "wait_for_build" || rs.Cursor.Attempt != 1 {
		t.Errorf("cursor = %+v, want wait_for_build attempt 1", rs.Cursor)
	}
	if rs.Terminal() {
		t.Errorf("run is not finished: the journal ends at a wait step")
	}
}

func TestCompatFormat1_PlanLegacyDecode(t *testing.T) {
	plan, err := ReadPlan(compatDir)
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.Format != 1 {
		t.Errorf("Format = %d, want 1", plan.Format)
	}
	w := plan.Workflow
	if w.MaxStepsRaw == nil || *w.MaxStepsRaw != 20 {
		t.Errorf("MaxStepsRaw = %v, want 20 (multi-word field zero-filled?)", w.MaxStepsRaw)
	}
	if w.MaxSteps != 20 {
		t.Errorf("MaxSteps = %d, want 20", w.MaxSteps)
	}
	sum := w.StepByID("summarize_build")
	if sum == nil || sum.AttemptsRaw == nil || *sum.AttemptsRaw != 2 || sum.Attempts != 2 {
		t.Errorf("summarize_build attempts lost: %+v", sum)
	}
	if sum.MaxVisits != 10 {
		t.Errorf("MaxVisits = %d, want 10", sum.MaxVisits)
	}
	if !sum.Writes.IsTyped || sum.Writes.Types["summary"] != "string" {
		t.Errorf("typed writes lost: %+v", sum.Writes)
	}
	if sum.Postcondition == nil || !sum.Postcondition.HasCommand || sum.Postcondition.Command != "true" {
		t.Errorf("postcondition lost: %+v", sum.Postcondition)
	}
	wait := w.StepByID("wait_for_build")
	if wait == nil || wait.Every != "5s" || wait.Timeout != "5m" || wait.Emits != "pairs" || wait.Outcomes["PASSED"] != "summarize_build" {
		t.Errorf("wait step lost fields: %+v", wait)
	}
	if arg := w.Args["force_result"]; arg.Type != "string" || arg.Default == nil {
		t.Errorf("arg decl lost: %+v", arg)
	}
	if w.Path != "/redacted/workflow.yaml" {
		t.Errorf("Path = %q", w.Path)
	}
}

func TestCompatFormat1_RedigestMatchesEquivalentYAML(t *testing.T) {
	plan, err := ReadPlan(compatDir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := spec.Load(filepath.Join(compatDir, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Digest(plan.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := Digest(want)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantDigest {
		gj, _ := os.ReadFile(filepath.Join(compatDir, "plan.json"))
		t.Errorf("re-digest of the decoded format-1 plan != digest of workflow.yaml\nplan.json: %s", gj)
	}
}
