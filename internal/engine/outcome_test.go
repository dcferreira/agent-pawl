package engine

import (
	"testing"

	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// TestResolveTarget is the outcome-resolution table test DESIGN.md §6 asks
// for: (step, outcome) -> (target, viaCatch), built as data rather than
// scattered ifs.
func TestResolveTarget(t *testing.T) {
	cases := []struct {
		name       string
		step       spec.Step
		outcome    string
		wantTarget string
		wantCatch  bool
		wantErr    bool
	}{
		{
			name:       "named outcome routes via outcomes map",
			step:       spec.Step{ID: "s", Outcomes: map[string]string{"FRESH": "wait_for_mr", "EXISTING": "choose_reviewer"}},
			outcome:    "FRESH",
			wantTarget: "wait_for_mr",
		},
		{
			name:       "success routes via next when no outcomes map",
			step:       spec.Step{ID: "s", Next: "done"},
			outcome:    "success",
			wantTarget: "done",
		},
		{
			name:       "failure explicit catch entry",
			step:       spec.Step{ID: "s", Next: "done", Catch: []spec.CatchRule{{On: "failure", Next: "fix_issues"}}},
			outcome:    "failure",
			wantTarget: "fix_issues",
			wantCatch:  true,
		},
		{
			name:       "failure default is blocked",
			step:       spec.Step{ID: "s", Next: "done"},
			outcome:    "failure",
			wantTarget: "blocked",
			wantCatch:  true,
		},
		{
			name:       "failure routed via outcomes map is still viaCatch",
			step:       spec.Step{ID: "s", Outcomes: map[string]string{"failure": "quarantine"}},
			outcome:    "failure",
			wantTarget: "quarantine",
			wantCatch:  true,
		},
		{
			name:       "exhausted routed by outcomes map",
			step:       spec.Step{ID: "s", Outcomes: map[string]string{"CLEAN": "done", "exhausted": "choose_reviewer"}},
			outcome:    "exhausted",
			wantTarget: "choose_reviewer",
			wantCatch:  true,
		},
		{
			name:       "exhausted default is blocked",
			step:       spec.Step{ID: "s", Next: "done"},
			outcome:    "exhausted",
			wantTarget: "blocked",
			wantCatch:  true,
		},
		{
			// Finding I4: catch: is not just for failure/exhausted — spec
			// §D defines it as an ordered list over any outcome.
			name: "non-reserved outcome routed via catch",
			step: spec.Step{ID: "s", Outcomes: map[string]string{"good": "done"},
				Catch: []spec.CatchRule{{On: "bad", Next: "blocked"}}},
			outcome:    "bad",
			wantTarget: "blocked",
			wantCatch:  true,
		},
		{
			name:    "non-reserved outcome with no route is an error",
			step:    spec.Step{ID: "s", Outcomes: map[string]string{"FRESH": "a"}},
			outcome: "OTHER",
			wantErr: true,
		},
		{
			name:    "no next and no outcomes is an error",
			step:    spec.Step{ID: "s"},
			outcome: "success",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, viaCatch, err := resolveTarget(&tc.step, tc.outcome)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveTarget(%q) = %q, nil; want an error", tc.outcome, target)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTarget(%q): unexpected error: %v", tc.outcome, err)
			}
			if target != tc.wantTarget || viaCatch != tc.wantCatch {
				t.Errorf("resolveTarget(%q) = (%q, %v), want (%q, %v)", tc.outcome, target, viaCatch, tc.wantTarget, tc.wantCatch)
			}
		})
	}
}
