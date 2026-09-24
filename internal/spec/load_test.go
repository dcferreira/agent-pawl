package spec

import (
	"errors"
	"strings"
	"testing"
)

// TestLoad_ErrorsNameTheStepID is the F9 regression test: a malformed
// polymorphic field (writes:, postcondition:) must produce a Load error that
// names the offending step, not just the file.
func TestLoad_ErrorsNameTheStepID(t *testing.T) {
	tests := []struct {
		file string
	}{
		{"testdata/f9_writes_bad_shape.yaml"},
		{"testdata/f9_postcondition_bad_shape.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			_, err := Load(tt.file)
			if err == nil {
				t.Fatalf("Load(%s): expected an error, got nil", tt.file)
			}
			if !strings.Contains(err.Error(), `step "a"`) {
				t.Fatalf("Load(%s) error = %q, want it to name step \"a\"", tt.file, err.Error())
			}
		})
	}
}

// TestLoad_UnknownFieldInGuardEntry checks that an unknown field inside a
// guards[] entry is rejected at Load time, the same way any other
// file-level unknown field is (KnownFields(true), Finding F6) — guards:
// has no custom UnmarshalYAML the way Step does, so this is not
// checkUnknownFields' job (that only inspects Step.UnknownFields).
func TestLoad_UnknownFieldInGuardEntry(t *testing.T) {
	_, err := Load("testdata/rule15_guard_unknown_field.yaml")
	if err == nil {
		t.Fatalf("Load: expected an error, got nil")
	}
	if !errors.Is(err, ErrParse) {
		t.Fatalf("Load error = %v, want it to wrap ErrParse", err)
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("Load error = %q, want it to name the unknown field %q", err.Error(), "frobnicate")
	}
}

func TestLoad_TidyHelloWorkflow(t *testing.T) {
	w, err := Load("testdata/tidy.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if w.Workflow != "tidy" || w.Start != "format" {
		t.Fatalf("got %+v", w)
	}
	if w.MaxSteps != DefaultMaxSteps {
		t.Fatalf("MaxSteps = %d, want default %d", w.MaxSteps, DefaultMaxSteps)
	}
	format := w.StepByID("format")
	if format == nil {
		t.Fatalf("step %q not found", "format")
	}
	if format.Attempts != DefaultAttempts {
		t.Fatalf("Attempts = %d, want default %d", format.Attempts, DefaultAttempts)
	}
	if format.MaxVisits != DefaultMaxVisits {
		t.Fatalf("MaxVisits = %d, want default %d", format.MaxVisits, DefaultMaxVisits)
	}
	if format.Emits != DefaultEmits {
		t.Fatalf("Emits = %q, want default %q", format.Emits, DefaultEmits)
	}

	report, err := Validate(w)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("expected zero errors, got %v", report.Errors)
	}
}

func TestLoad_ValidExample(t *testing.T) {
	w, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	report, err := Validate(w)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("expected zero errors, got %v", report.Errors)
	}
	if report.SoftCount != 1 || len(report.SoftStepIDs) != 1 || report.SoftStepIDs[0] != "build" {
		t.Fatalf("soft census = count %d steps %v, want count 1 [build]", report.SoftCount, report.SoftStepIDs)
	}
}

// TestLoad_MergePostcondition is the F3 regression test: a YAML merge key
// ("<<: *anchor") in a map-form postcondition: must resolve, not surface as
// an unknown key nor drop the merged-in keys.
func TestLoad_MergePostcondition(t *testing.T) {
	w, err := Load("testdata/merge_postcondition.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b := w.StepByID("b")
	if b == nil || b.Postcondition == nil {
		t.Fatalf("step b or its postcondition missing: %+v", w)
	}
	if !b.Postcondition.HasCommand || b.Postcondition.Command != "true" {
		t.Fatalf("merged command = %+v, want HasCommand=true Command=\"true\"", b.Postcondition)
	}
	if !b.Postcondition.Soft {
		t.Fatalf("Soft = false, want true")
	}
	if len(b.Postcondition.UnknownKeys) != 0 {
		t.Fatalf("UnknownKeys = %v, want none (merge key must not appear as an unknown key)", b.Postcondition.UnknownKeys)
	}

	report, err := Validate(w)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("expected zero errors, got %v", report.Errors)
	}
}

// TestLoad_MergeWrites is the F3 regression test for writes:.
func TestLoad_MergeWrites(t *testing.T) {
	w, err := Load("testdata/merge_writes.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b := w.StepByID("b")
	if b == nil {
		t.Fatalf("step b missing")
	}
	if !b.Writes.IsTyped {
		t.Fatalf("Writes.IsTyped = false, want true")
	}
	if len(b.Writes.Keys) != 1 || b.Writes.Keys[0] != "count" {
		t.Fatalf("Writes.Keys = %v, want [count] (merge key must not silently drop the merged-in key)", b.Writes.Keys)
	}
	if b.Writes.Types["count"] != "integer" {
		t.Fatalf("Writes.Types[count] = %q, want integer", b.Writes.Types["count"])
	}

	report, err := Validate(w)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("expected zero errors, got %v", report.Errors)
	}
}

// TestStep_SoftIsMergedNotDuplicated is the F4 regression test: Step no
// longer exposes its own authored "Soft" field (a second, disagreeing
// source of truth); Postcondition.Soft is the only place to read it, and it
// must be true whether soft: was authored as a step-level sibling of
// postcondition: or nested inside a map-form postcondition:.
func TestStep_SoftIsMergedNotDuplicated(t *testing.T) {
	w, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	build := w.StepByID("build")
	if build == nil || build.Postcondition == nil {
		t.Fatalf("step build or its postcondition missing")
	}
	if !build.Postcondition.Soft {
		t.Fatalf("Postcondition.Soft = false for a step-level soft: true, want true")
	}
	// Step has no exported Soft field to disagree with Postcondition.Soft;
	// this is a compile-time guarantee as much as a runtime one, but assert
	// the merged truth here so a regression is caught either way.
}

// TestValidate_DefensiveDefaults is the F8 regression test: Validate must
// not assume Load ran. A *Workflow built directly (as a future journal
// round-trip in Task 5 will do) has AttemptsRaw/MaxVisitsRaw/MaxStepsRaw and
// Attempts/MaxVisits/MaxSteps all at their Go zero values; Validate must
// apply defaults itself rather than reporting a spurious rule 12 violation
// on every step.
func TestValidate_DefensiveDefaults(t *testing.T) {
	w := &Workflow{
		Workflow: "defensive",
		Start:    "a",
		Path:     "in-memory",
		Steps: []Step{
			{
				ID:            "a",
				Kind:          "deterministic",
				Run:           "echo hi",
				Next:          "done",
				Postcondition: nil,
			},
		},
		Terminal: map[string]Terminal{"done": {Status: "ok"}},
	}

	report, err := Validate(w)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("expected zero errors on a Load-less Workflow with valid *Raw fields, got %v", report.Errors)
	}
	if w.MaxSteps != DefaultMaxSteps {
		t.Fatalf("MaxSteps = %d, want defensively-applied default %d", w.MaxSteps, DefaultMaxSteps)
	}
	if w.Steps[0].Attempts != DefaultAttempts {
		t.Fatalf("Attempts = %d, want defensively-applied default %d", w.Steps[0].Attempts, DefaultAttempts)
	}
	if w.Steps[0].MaxVisits != DefaultMaxVisits {
		t.Fatalf("MaxVisits = %d, want defensively-applied default %d", w.Steps[0].MaxVisits, DefaultMaxVisits)
	}
}

// TestValidate_InvalidAttemptsClamped is the other half of F8: attempts: 0
// must still fire rule 13 (using AttemptsRaw as the witness), but the
// resolved Attempts field a later package reads must be clamped to the
// default rather than staying at the invalid 0.
func TestValidate_InvalidAttemptsClamped(t *testing.T) {
	w, err := Load("testdata/rule13_attempts_zero.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := w.StepByID("a")
	if a == nil {
		t.Fatalf("step a missing")
	}
	if a.Attempts != DefaultAttempts {
		t.Fatalf("Attempts = %d, want clamped to default %d (AttemptsRaw=%v is the rule-13 witness)", a.Attempts, DefaultAttempts, a.AttemptsRaw)
	}
	if a.AttemptsRaw == nil || *a.AttemptsRaw != 0 {
		t.Fatalf("AttemptsRaw = %v, want a pointer to 0 (the authored, invalid value)", a.AttemptsRaw)
	}
}

// TestLoad_WaitEveryDefault is the wait-kind analogue of the Emits default:
// a wait step with no every: gets DefaultEvery ("60s"), applied by
// applyDefaults the same way Attempts/MaxVisits/Emits already are.
func TestLoad_WaitEveryDefault(t *testing.T) {
	w, err := Load("testdata/wait_valid_next.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	step := w.StepByID("w")
	if step == nil {
		t.Fatalf("step %q not found", "w")
	}
	if step.Every != DefaultEvery {
		t.Fatalf("Every = %q, want default %q", step.Every, DefaultEvery)
	}
}

func TestLoad_ExplicitOverridesDefault(t *testing.T) {
	w, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if w.MaxSteps != 50 {
		t.Fatalf("MaxSteps = %d, want explicit 50", w.MaxSteps)
	}
	fix := w.StepByID("fix")
	if fix.Attempts != 2 {
		t.Fatalf("Attempts = %d, want explicit 2", fix.Attempts)
	}
}
