package spec

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestValidate_GoldenMessages golden-file tests the exact validator error
// messages, one fixture per implemented rule (DESIGN.md §6): every message
// must name the file, the step id, the rule, and the fix.
func TestValidate_GoldenMessages(t *testing.T) {
	tests := []struct {
		name string
		file string
		want []string
	}{
		{
			name: "rule1: unknown next target",
			file: "rule1_bad_target.yaml",
			want: []string{
				`testdata/rule1_bad_target.yaml: step "a": rule 1: next: "nowhere" names a step or terminal that does not exist; declare step "nowhere", add it under terminal:, or fix the typo`,
			},
		},
		{
			name: "rule2: unreachable step",
			file: "rule2_unreachable.yaml",
			want: []string{
				`testdata/rule2_unreachable.yaml: step "orphan": rule 2: is unreachable from start: "a"; add an edge to it or remove it`,
			},
		},
		{
			name: "rule3: no outgoing edge",
			file: "rule3_no_route.yaml",
			want: []string{
				`testdata/rule3_no_route.yaml: step "a": rule 3: has no next: or outcomes:; add one naming its successor step or a terminal`,
			},
		},
		{
			name: "rule3b: wait missing timeout route (plus kind rejection)",
			file: "rule3b_wait_missing_timeout_route.yaml",
			want: []string{
				`testdata/rule3b_wait_missing_timeout_route.yaml: step "w": kind "wait" is not implemented in this build (milestone 1 MVP covers deterministic and agentic)`,
				`testdata/rule3b_wait_missing_timeout_route.yaml: step "w": rule 3b: has no route for outcome "timeout"; add outcomes: {timeout: <step-or-terminal>, ...}`,
			},
		},
		{
			name: "rule4: undeclared key",
			file: "rule4_undeclared_key.yaml",
			want: []string{
				`testdata/rule4_undeclared_key.yaml: step "a": rule 4: run uses ${nope}, which is not declared in state:/args: and is not an engine pseudo-key; declare "nope" under state: or args:, or fix the typo`,
			},
		},
		{
			name: "rule4: substitution used where it does not apply",
			file: "rule4_no_subst_field.yaml",
			want: []string{
				`testdata/rule4_no_subst_field.yaml: step "a": rule 1: next: "${bad}" names a step or terminal that does not exist; declare step "${bad}", add it under terminal:, or fix the typo`,
				`testdata/rule4_no_subst_field.yaml: step "a": rule 4: next does not support ${...} substitution; remove it ("${bad}")`,
			},
		},
		{
			name: "rule5: writes on an args key",
			file: "rule5_writes_arg.yaml",
			want: []string{
				`testdata/rule5_writes_arg.yaml: step "a": rule 5: writes: "target", but "target" is declared under args: and args are read-only; remove it from writes: or declare it under state: instead`,
			},
		},
		{
			name: "rule6: agentic step with no postcondition",
			file: "rule6_agentic_no_postcondition.yaml",
			want: []string{
				`testdata/rule6_agentic_no_postcondition.yaml: step "a": rule 6: kind: agentic requires postcondition: (soft: true is not an exemption from declaring one); add a postcondition:`,
			},
		},
		{
			name: "rule9: agentic step with author-named outcomes",
			file: "rule9_agentic_outcomes.yaml",
			want: []string{
				`testdata/rule9_agentic_outcomes.yaml: step "a": rule 9: kind: agentic can only produce success/failure, not author-named outcomes; remove outcomes: and route from a following deterministic step that reads this step's writes: and prints a token`,
			},
		},
		{
			name: "rule9b: subagent_args not a map",
			file: "rule9b_subagent_args_not_map.yaml",
			want: []string{
				`testdata/rule9b_subagent_args_not_map.yaml: step "a": rule 9b: subagent_args: must be a map; change it to key: value pairs`,
			},
		},
		{
			name: "rule10: invalid emits",
			file: "rule10_bad_emits.yaml",
			want: []string{
				`testdata/rule10_bad_emits.yaml: step "a": rule 10: emits: "xml" is not json or pairs; use one of the two`,
			},
		},
		{
			name: "rule11: undeclared write",
			file: "rule11_undeclared_write.yaml",
			want: []string{
				`testdata/rule11_undeclared_write.yaml: step "a": rule 11: writes: "nope", which is not declared in state:; add state: {nope: {type: ...}} or fix the typo`,
			},
		},
		{
			name: "rule11: write type conflicts with state declaration",
			file: "rule11_type_conflict.yaml",
			want: []string{
				`testdata/rule11_type_conflict.yaml: step "a": rule 11: writes: count: {type: string} conflicts with state: count: {type: integer}; make the types match`,
			},
		},
		{
			name: "rule12: max_steps not positive",
			file: "rule12_max_steps_zero.yaml",
			want: []string{
				`testdata/rule12_max_steps_zero.yaml: rule 12: max_steps: 0 is not a positive integer; use a value of 1 or more`,
			},
		},
		{
			name: "rule12: cycle whose cap can never bind",
			file: "rule12_cycle_cap_never_binds.yaml",
			want: []string{
				`testdata/rule12_cycle_cap_never_binds.yaml: rule 12: cycle [a, b] has max_visits: raised above max_steps: 5 on every step; a cap that can never bind. Lower at least one step's max_visits: to at most max_steps:`,
			},
		},
		{
			name: "rule13: attempts less than 1",
			file: "rule13_attempts_zero.yaml",
			want: []string{
				`testdata/rule13_attempts_zero.yaml: step "a": rule 13: attempts: 0 is less than 1; use a value of 1 or more`,
			},
		},
		{
			name: "rule14: two postcondition alternatives at once",
			file: "rule14_two_alts.yaml",
			want: []string{
				`testdata/rule14_two_alts.yaml: step "a": rule 14: postcondition: must use exactly one of command, all_set, equals (found 2); remove the extra key(s) or add the missing one`,
			},
		},
		{
			name: "rule14: unknown postcondition key",
			file: "rule14_unknown_key.yaml",
			want: []string{
				`testdata/rule14_unknown_key.yaml: step "a": rule 14: postcondition: uses unknown key(s) bogus; use only command, all_set, equals, and optional soft`,
			},
		},
		{
			name: "R3: wait is not implemented in this build",
			file: "r3_wait_not_implemented.yaml",
			want: []string{
				`testdata/r3_wait_not_implemented.yaml: step "w": kind "wait" is not implemented in this build (milestone 1 MVP covers deterministic and agentic)`,
			},
		},
		{
			name: "R3: human is not implemented in this build",
			file: "r3_human_not_implemented.yaml",
			want: []string{
				`testdata/r3_human_not_implemented.yaml: step "h": kind "human" is not implemented in this build (milestone 1 MVP covers deterministic and agentic)`,
			},
		},
		{
			name: "R3: parallel is reserved for Milestone 3",
			file: "r3_parallel_reserved.yaml",
			want: []string{
				`testdata/r3_parallel_reserved.yaml: step "p": kind: parallel is reserved for Milestone 3`,
			},
		},
		{
			name: "R7: retry is not implemented in this build",
			file: "r7_retry_not_implemented.yaml",
			want: []string{
				`testdata/r7_retry_not_implemented.yaml: step "a": retry: is not implemented in this build; remove the retry: block`,
			},
		},
		{
			name: "R8: guards are not implemented in this build",
			file: "r8_guards_rejected.yaml",
			want: []string{
				`testdata/r8_guards_rejected.yaml: guards: is not implemented in this build; remove the guards: block`,
			},
		},
		{
			name: "R8: invariants are not implemented in this build",
			file: "r8_invariants_rejected.yaml",
			want: []string{
				`testdata/r8_invariants_rejected.yaml: invariants: is not implemented in this build; remove the invariants: block`,
			},
		},
		{
			name: "F1: empty {} document",
			file: "f1_empty_object.yaml",
			want: []string{
				`testdata/f1_empty_object.yaml: workflow: is required; add workflow: <name>`,
				`testdata/f1_empty_object.yaml: start: is required; add start: <step-id>`,
				`testdata/f1_empty_object.yaml: steps: must declare at least one step; add one`,
			},
		},
		{
			name: "F1: zero-byte file",
			file: "f1_empty_file.yaml",
			want: []string{
				`testdata/f1_empty_file.yaml: workflow: is required; add workflow: <name>`,
				`testdata/f1_empty_file.yaml: start: is required; add start: <step-id>`,
				`testdata/f1_empty_file.yaml: steps: must declare at least one step; add one`,
			},
		},
		{
			name: "F2: missing start: produces one clear error, no rule 2 cascade",
			file: "f2_missing_start.yaml",
			want: []string{
				`testdata/f2_missing_start.yaml: start: is required; add start: <step-id>`,
			},
		},
		{
			name: "F2: start: names a step that does not exist, no rule 2 cascade",
			file: "f2_bad_start.yaml",
			want: []string{
				`testdata/f2_bad_start.yaml: start: "nope" does not name a declared step; declare step "nope" or fix the typo`,
			},
		},
		{
			name: "F5a: duplicate step id",
			file: "f5a_duplicate_ids.yaml",
			want: []string{
				`testdata/f5a_duplicate_ids.yaml: step "a": id "a" is declared more than once; step ids must be unique — rename one of them`,
			},
		},
		{
			name: "F5b: next: and outcomes: both present",
			file: "f5b_next_and_outcomes.yaml",
			want: []string{
				`testdata/f5b_next_and_outcomes.yaml: step "a": has both next: and outcomes:; these are mutually exclusive — keep only one`,
			},
		},
		{
			name: "F5c: deterministic step missing run:",
			file: "f5c_deterministic_missing_run.yaml",
			want: []string{
				`testdata/f5c_deterministic_missing_run.yaml: step "a": kind: deterministic requires run:; add a run: command`,
			},
		},
		{
			name: "F5c: agentic step missing description:",
			file: "f5c_agentic_missing_description.yaml",
			want: []string{
				`testdata/f5c_agentic_missing_description.yaml: step "a": kind: agentic requires description:; add a description: of the intent, constraints and definition of done`,
			},
		},
		{
			name: "F6: unknown step field with a spelling suggestion",
			file: "f6_unknown_field.yaml",
			want: []string{
				`testdata/f6_unknown_field.yaml: step "a": has unknown field "nxt" (did you mean "next"?); rename it or remove it`,
			},
		},
		{
			name: "F7: agentic step with no writes: at all",
			file: "f7_agentic_missing_writes.yaml",
			want: []string{
				`testdata/f7_agentic_missing_writes.yaml: step "a": kind: agentic requires a typed writes: map — it is the subagent's return schema; add writes: {<key>: {type: string|integer|json}} declaring each key the subagent returns`,
			},
		},
		{
			name: "F7: agentic step with an untyped list writes:",
			file: "f7_agentic_untyped_list.yaml",
			want: []string{
				`testdata/f7_agentic_untyped_list.yaml: step "a": kind: agentic requires a typed writes: map — it is the subagent's return schema; add writes: {<key>: {type: string|integer|json}} declaring each key the subagent returns`,
			},
		},
		{
			name: "F7: agentic step with an empty typed writes: map",
			file: "f7_agentic_empty_typed_map.yaml",
			want: []string{
				`testdata/f7_agentic_empty_typed_map.yaml: step "a": kind: agentic requires a typed writes: map — it is the subagent's return schema; add writes: {<key>: {type: string|integer|json}} declaring each key the subagent returns`,
			},
		},
		{
			name: "F7: agentic step with a writes: key that declares no type",
			file: "f7_agentic_no_type_declared.yaml",
			want: []string{
				`testdata/f7_agentic_no_type_declared.yaml: step "a": kind: agentic requires a typed writes: map — it is the subagent's return schema; add writes: {<key>: {type: string|integer|json}} declaring each key the subagent returns`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := Load(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			report, err := Validate(w)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !reflect.DeepEqual(report.Errors, tt.want) {
				t.Fatalf("Errors =\n%v\nwant\n%v", report.Errors, tt.want)
			}
		})
	}
}

func TestValidate_ValidWorkflowsHaveZeroErrors(t *testing.T) {
	for _, file := range []string{"tidy.yaml", "valid.yaml"} {
		t.Run(file, func(t *testing.T) {
			w, err := Load(filepath.Join("testdata", file))
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
		})
	}
}
