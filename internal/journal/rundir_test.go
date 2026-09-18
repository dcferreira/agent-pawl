package journal

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

// wantSpecWorkflowFieldCount and wantSpecStepFieldCount are round-2 finding
// N4: digestWorkflow/digestStep hand-enumerate every spec.Workflow/spec.Step
// field they mean to carry (or deliberately omit — see the comment on
// digestWorkflow), so a newly authored field added to either upstream type
// would otherwise be silently excluded from Digest, and a genuinely changed
// workflow would then resume as unchanged: the C2 failure mode wearing a
// different hat. Bump these two constants only after deciding, for the new
// field, whether it belongs in digestWorkflow/digestStep too.
const (
	wantSpecWorkflowFieldCount = 12
	wantSpecStepFieldCount     = 26
)

func TestDigestProjection_PinnedAgainstSpecFieldCount(t *testing.T) {
	if got := reflect.TypeOf(spec.Workflow{}).NumField(); got != wantSpecWorkflowFieldCount {
		t.Errorf("spec.Workflow has %d fields, want %d (pinned) — a field was added or removed; "+
			"decide whether it belongs in digestWorkflow (rundir.go), then update wantSpecWorkflowFieldCount", got, wantSpecWorkflowFieldCount)
	}
	if got := reflect.TypeOf(spec.Step{}).NumField(); got != wantSpecStepFieldCount {
		t.Errorf("spec.Step has %d fields, want %d (pinned) — a field was added or removed; "+
			"decide whether it belongs in digestStep (rundir.go), then update wantSpecStepFieldCount", got, wantSpecStepFieldCount)
	}
}

func TestStateBase_EnvOverride(t *testing.T) {
	t.Setenv(EnvStateDir, "/tmp/pawl-test-state")
	if got, want := StateBase(), "/tmp/pawl-test-state"; got != want {
		t.Errorf("StateBase() = %q, want %q", got, want)
	}
}

func TestRunDir_Layout(t *testing.T) {
	t.Setenv(EnvStateDir, "/state")
	got := RunDir("/home/user/proj", "ship", "r1")
	want := filepath.Join("/state", Slug("/home/user/proj"), "ship", "r1")
	if got != want {
		t.Errorf("RunDir = %q, want %q", got, want)
	}
}

func TestCreateRunDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvStateDir, base)
	dir, err := CreateRunDir("/home/user/proj", "ship", "r1")
	if err != nil {
		t.Fatalf("CreateRunDir: %v", err)
	}
	if dir != RunDir("/home/user/proj", "ship", "r1") {
		t.Errorf("CreateRunDir returned %q", dir)
	}
	// Idempotent.
	if _, err := CreateRunDir("/home/user/proj", "ship", "r1"); err != nil {
		t.Fatalf("CreateRunDir (again): %v", err)
	}
}

func TestPlan_WriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w := &spec.Workflow{
		Workflow: "ship",
		Start:    "build",
		Steps: []spec.Step{
			{ID: "build", Kind: "deterministic", Run: "echo hi"},
		},
	}
	written, err := WritePlan(dir, w)
	if err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	if written.Digest == "" {
		t.Fatal("WritePlan produced an empty digest")
	}

	read, err := ReadPlan(dir)
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if read.Digest != written.Digest {
		t.Errorf("ReadPlan digest = %q, want %q", read.Digest, written.Digest)
	}
	if read.Workflow.Workflow != "ship" || read.Workflow.Start != "build" {
		t.Errorf("ReadPlan workflow round-trip wrong: %+v", read.Workflow)
	}
}

func TestDigest_StableAndSensitiveToChange(t *testing.T) {
	w1 := &spec.Workflow{Workflow: "a", Steps: []spec.Step{{ID: "s1", Kind: "deterministic", Run: "true"}}}
	w2 := &spec.Workflow{Workflow: "a", Steps: []spec.Step{{ID: "s1", Kind: "deterministic", Run: "true"}}}
	w3 := &spec.Workflow{Workflow: "a", Steps: []spec.Step{{ID: "s1", Kind: "deterministic", Run: "false"}}}

	d1, err := Digest(w1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Digest(w2)
	if err != nil {
		t.Fatal(err)
	}
	d3, err := Digest(w3)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("Digest not stable for identical content: %q vs %q", d1, d2)
	}
	if d1 == d3 {
		t.Errorf("Digest identical for different content: %q", d1)
	}
}

// TestDigest_IgnoresPath is C2: the same parsed content loaded from two
// different paths (e.g. "workflow.yaml" from one cwd, "/abs/path/workflow.yaml" from
// another) must digest identically, or resume step 2 of DESIGN.md §4
// refuses a live run over nothing but where `pawl` happened to be invoked
// from.
func TestDigest_IgnoresPath(t *testing.T) {
	w1 := &spec.Workflow{Workflow: "a", Path: "workflow.yaml", Steps: []spec.Step{{ID: "s1", Kind: "deterministic", Run: "true"}}}
	w2 := &spec.Workflow{Workflow: "a", Path: "/abs/path/workflow.yaml", Steps: []spec.Step{{ID: "s1", Kind: "deterministic", Run: "true"}}}

	d1, err := Digest(w1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Digest(w2)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("Digest depends on Path: %q (Path=%q) vs %q (Path=%q)", d1, w1.Path, d2, w2.Path)
	}
}

// TestDigest_IgnoresResolvedDefaults is C2's second half: MaxSteps/Attempts/
// MaxVisits are computed from their *Raw witnesses by applyDefaults, not
// authored content in their own right. Two Workflow values that differ only
// in those resolved fields (as if applyDefaults' logic changed between
// engine versions, with the same *Raw fields — the same authored file) must
// digest identically; the *Raw fields are what actually varies with
// authored content, and Digest must still be sensitive to them.
func TestDigest_IgnoresResolvedDefaults(t *testing.T) {
	raw := 5
	w1 := &spec.Workflow{Workflow: "a", MaxStepsRaw: &raw, MaxSteps: 5,
		Steps: []spec.Step{{ID: "s1", Kind: "deterministic", Run: "true", AttemptsRaw: &raw, Attempts: 5}}}
	w2 := &spec.Workflow{Workflow: "a", MaxStepsRaw: &raw, MaxSteps: 999, // hypothetically re-defaulted differently
		Steps: []spec.Step{{ID: "s1", Kind: "deterministic", Run: "true", AttemptsRaw: &raw, Attempts: 999}}}

	d1, err := Digest(w1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Digest(w2)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("Digest depends on resolved MaxSteps/Attempts, not just their *Raw witnesses: %q vs %q", d1, d2)
	}

	other := 6
	w3 := &spec.Workflow{Workflow: "a", MaxStepsRaw: &other, MaxSteps: 6,
		Steps: []spec.Step{{ID: "s1", Kind: "deterministic", Run: "true", AttemptsRaw: &raw, Attempts: 5}}}
	d3, err := Digest(w3)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d3 {
		t.Error("Digest did not change when MaxStepsRaw (authored content) changed")
	}
}

func TestWriteStatus(t *testing.T) {
	dir := t.TempDir()
	rs, err := Replay(completedRunFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteStatus(dir, "r1", "ship", "/home/user/proj", rs); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}
}
