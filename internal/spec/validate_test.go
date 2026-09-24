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
			name: "rule3b: wait missing timeout route",
			file: "rule3b_wait_missing_timeout_route.yaml",
			want: []string{
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
			name: "rule18: untagged bang context entry",
			file: "rule18_untagged_bang_context.yaml",
			want: []string{
				`testdata/rule18_untagged_bang_context.yaml: step "a": rule 18: context[0]: "!git diff main...HEAD" is a plain string starting with "!", which names a FILE PATH, not a command; tag it with !cmd to run it as a command: !cmd "git diff main...HEAD"`,
			},
		},
		{
			name: "rule18: unquoted custom-tagged bang context entry",
			file: "rule18_unquoted_bang_tag.yaml",
			want: []string{
				`testdata/rule18_unquoted_bang_tag.yaml: step "a": rule 18: context[0]: tagged !git "diff main", which is not a recognised command form; only the YAML tag !cmd runs a command — use !cmd "git diff main" instead`,
			},
		},
		{
			name: "rule18: quoted string with the !cmd tag inside the quotes",
			file: "rule18_quoted_bang_cmd.yaml",
			want: []string{
				`testdata/rule18_quoted_bang_cmd.yaml: step "a": rule 18: context[0]: "!cmd git diff main" is a plain string with the !cmd tag INSIDE the quotes, which names a FILE PATH, not a command; the tag must sit outside the quotes: !cmd "git diff main"`,
			},
		},
		{
			name: "rule7: human step with no timeout: (plus no route for the timeout outcome)",
			file: "rule7_human_missing_timeout.yaml",
			want: []string{
				`testdata/rule7_human_missing_timeout.yaml: step "h": rule 3b: has no route for outcome "timeout"; add outcomes: {timeout: <step-or-terminal>, ...}`,
				`testdata/rule7_human_missing_timeout.yaml: step "h": rule 7: kind: human requires timeout:; add a timeout: duration (e.g. "5m", "1h")`,
			},
		},
		{
			name: "rule7: wait step with no timeout: (also covered by wait's own wait_missing_timeout.yaml test)",
			file: "rule7_wait_missing_timeout.yaml",
			want: []string{
				`testdata/rule7_wait_missing_timeout.yaml: step "w": rule 3b: has no route for outcome "timeout"; add outcomes: {timeout: <step-or-terminal>, ...}`,
				`testdata/rule7_wait_missing_timeout.yaml: step "w": rule 7: kind: wait requires timeout:; add a timeout: duration (e.g. "5m", "1h")`,
			},
		},
		{
			name: "rule8: human step with both options: and options_from:",
			file: "rule8_options_and_options_from.yaml",
			want: []string{
				`testdata/rule8_options_and_options_from.yaml: step "h": rule 8: kind: human sets both options: and options_from:; keep only one`,
			},
		},
		{
			name: "rule8: human step with neither options: nor options_from:",
			file: "rule8_no_options.yaml",
			want: []string{
				`testdata/rule8_no_options.yaml: step "h": rule 8: kind: human requires exactly one of options: or options_from:; add one`,
			},
		},
		{
			name: "rule8: unrouted static options",
			file: "rule8_unrouted_option.yaml",
			want: []string{
				`testdata/rule8_unrouted_option.yaml: step "h": rule 8: options: "bob" has no route in outcomes:; add outcomes: {bob: <step-or-terminal>, ...}`,
				`testdata/rule8_unrouted_option.yaml: step "h": rule 8: options: "skip" has no route in outcomes:; add outcomes: {skip: <step-or-terminal>, ...}`,
			},
		},
		{
			name: "rule8: multi: on a non-human kind",
			file: "rule8_multi_on_deterministic.yaml",
			want: []string{
				`testdata/rule8_multi_on_deterministic.yaml: step "a": rule 8: multi: is only valid on kind: human; remove it`,
			},
		},
		{
			name: "rule8: chosen: route with no writes:",
			file: "rule8_chosen_no_writes.yaml",
			want: []string{
				`testdata/rule8_chosen_no_writes.yaml: step "h": rule 8: outcomes: {chosen: ...} requires writes: with exactly one key (found 0); add writes: [<key>] naming the key that receives the answer`,
			},
		},
		{
			name: "rule8: chosen: route with writes: of more than one key",
			file: "rule8_chosen_two_writes.yaml",
			want: []string{
				`testdata/rule8_chosen_two_writes.yaml: step "h": rule 8: outcomes: {chosen: ...} requires writes: with exactly one key (found 2); add writes: [<key>] naming the key that receives the answer`,
			},
		},
		{
			name: "rule8: options_from: without writes:",
			file: "rule8_options_from_no_writes.yaml",
			want: []string{
				`testdata/rule8_options_from_no_writes.yaml: step "h": rule 8: options_from: requires writes: with exactly one key (found 0); add writes: [<key>] naming the key that receives the answer`,
			},
		},
		{
			name: "rule8: multi: true without writes:",
			file: "rule8_multi_true_no_writes.yaml",
			want: []string{
				`testdata/rule8_multi_true_no_writes.yaml: step "h": rule 8: multi: true requires writes: with exactly one key (found 0); add writes: [<key>] naming the key that receives the answer`,
			},
		},
		{
			name: "R3: wait is now implemented and validates clean",
			file: "r3_wait_not_implemented.yaml",
			want: nil,
		},
		{
			name: "parallel: branches: requires at least 2 entries",
			file: "parallel_branches_too_few.yaml",
			want: []string{
				`testdata/parallel_branches_too_few.yaml: step "p": kind: parallel requires branches: with at least 2 entries`,
			},
		},
		{
			name: "parallel: branch does not name a declared step",
			file: "parallel_branch_undeclared.yaml",
			want: []string{
				`testdata/parallel_branch_undeclared.yaml: step "p": branches[1]: "nope" does not name a declared step`,
			},
		},
		{
			name: "parallel: branch has a kind that is not deterministic or agentic",
			file: "parallel_branch_wrong_kind.yaml",
			want: []string{
				`testdata/parallel_branch_wrong_kind.yaml: step "p": branches[1]: step "w" has kind "wait", but a parallel branch must be deterministic or agentic (no nesting)`,
			},
		},
		{
			name: "parallel: branch listed more than once",
			file: "parallel_branch_duplicate.yaml",
			want: []string{
				`testdata/parallel_branch_duplicate.yaml: step "p": branches[1]: step "b1" is listed more than once`,
			},
		},
		{
			name: "parallel: branch claimed by two parallel steps",
			file: "parallel_branch_double_owned.yaml",
			want: []string{
				`testdata/parallel_branch_double_owned.yaml: step "p2": branches[0]: step "b1" is already claimed as a branch by parallel step "p1"; a step may be a branch of only one parallel step`,
			},
		},
		{
			name: "parallel: branch is the workflow's start step",
			file: "parallel_branch_is_start.yaml",
			want: []string{
				`testdata/parallel_branch_is_start.yaml: step "p": branches[0]: step "b1" is start:, but a parallel branch may not be the workflow's start step`,
				`testdata/parallel_branch_is_start.yaml: step "p": rule 2: is unreachable from start: "b1"; add an edge to it or remove it`,
				`testdata/parallel_branch_is_start.yaml: step "b2": rule 2: is unreachable from start: "b1"; add an edge to it or remove it`,
			},
		},
		{
			name: "parallel: branch declares next:",
			file: "parallel_branch_has_next.yaml",
			want: []string{
				`testdata/parallel_branch_has_next.yaml: step "b1": declares next:, but it is a branch of parallel step "p", which owns routing and retry for the whole group; remove next:`,
			},
		},
		{
			name: "parallel: branch declares attempts:",
			file: "parallel_branch_has_attempts.yaml",
			want: []string{
				`testdata/parallel_branch_has_attempts.yaml: step "b1": declares attempts:, but it is a branch of parallel step "p", which owns routing and retry for the whole group; remove attempts:`,
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
			name: "rule15: guard missing id",
			file: "rule15_guard_missing_id.yaml",
			want: []string{
				`testdata/rule15_guard_missing_id.yaml: guards[0]: id: is required; add a unique id`,
			},
		},
		{
			name: "rule15: guard duplicate id",
			file: "rule15_guard_dup_id.yaml",
			want: []string{
				`testdata/rule15_guard_dup_id.yaml: guard "dup" is declared more than once; guard ids must be unique — rename one of them`,
			},
		},
		{
			name: "rule15: guard missing match",
			file: "rule15_guard_missing_match.yaml",
			want: []string{
				`testdata/rule15_guard_missing_match.yaml: guard "no-match": match: is required; add a match: regexp`,
			},
		},
		{
			name: "rule15: guard match does not compile",
			file: "rule15_guard_bad_match.yaml",
			want: []string{
				`testdata/rule15_guard_bad_match.yaml: guard "bad-regexp": match: "git push (" does not compile as a regexp: error parsing regexp: missing closing ): ` + "`git push (`",
			},
		},
		{
			name: "rule15: guard missing only_in",
			file: "rule15_guard_missing_only_in.yaml",
			want: []string{
				`testdata/rule15_guard_missing_only_in.yaml: guard "no-only-in": only_in: is required; use only_in: [] to deny it in every step`,
			},
		},
		{
			name: "rule15: guard id is not a valid identifier",
			file: "rule15_guard_bad_id_format.yaml",
			want: []string{
				`testdata/rule15_guard_bad_id_format.yaml: guard "../escaped": id is not a valid guard id; use only letters, digits, "_" and "-", starting with a letter or digit — rename the guard`,
			},
		},
		{
			name: "rule15: guard only_in names a missing step",
			file: "rule15_guard_only_in_missing_step.yaml",
			want: []string{
				`testdata/rule15_guard_only_in_missing_step.yaml: guard "bad-only-in": rule 15: only_in: "nope" does not name a declared step; declare step "nope" or fix the typo`,
			},
		},
		{
			name: "rule15: guard match matches the empty string",
			file: "rule15_guard_empty_match.yaml",
			want: []string{
				`testdata/rule15_guard_empty_match.yaml: guard "matches-everything": match: "git push|" matches the empty string, so it would match (and deny) every command; use a pattern that requires something concrete`,
			},
		},
		{
			name: "rule15: guard missing id, match and only_in together reports one error per field",
			file: "rule15_guard_missing_all_fields.yaml",
			want: []string{
				`testdata/rule15_guard_missing_all_fields.yaml: guards[0]: id: is required; add a unique id`,
				`testdata/rule15_guard_missing_all_fields.yaml: guards[0]: match: is required; add a match: regexp`,
				`testdata/rule15_guard_missing_all_fields.yaml: guards[0]: only_in: is required; use only_in: [] to deny it in every step`,
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
			name: "N1: step id is not identifier-like",
			file: "n1_bad_step_id.yaml",
			want: []string{
				`testdata/n1_bad_step_id.yaml: step "../../escaped": id "../../escaped" is not a valid step id; use only letters, digits, "_" and "-", starting with a letter or digit — rename the step`,
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
		{
			name: "wait: minimal valid step (outcomes: form) validates clean",
			file: "wait_valid_minimal.yaml",
			want: nil,
		},
		{
			name: "wait: minimal valid step (next: form) validates clean",
			file: "wait_valid_next.yaml",
			want: nil,
		},
		{
			name: "wait: missing poll:",
			file: "wait_missing_poll.yaml",
			want: []string{
				`testdata/wait_missing_poll.yaml: step "w": kind: wait requires poll:; add a poll: command`,
			},
		},
		{
			name: "wait: missing timeout:",
			file: "wait_missing_timeout.yaml",
			want: []string{
				`testdata/wait_missing_timeout.yaml: step "w": rule 7: kind: wait requires timeout:; add a timeout: duration (e.g. "5m", "1h")`,
			},
		},
		{
			name: "wait: every: is not a valid duration",
			file: "wait_bad_every.yaml",
			want: []string{
				`testdata/wait_bad_every.yaml: step "w": every: "soon" is not a valid duration; use Go duration syntax, e.g. "60s", "5m"`,
			},
		},
		{
			name: "wait: timeout: is not a valid duration",
			file: "wait_bad_timeout.yaml",
			want: []string{
				`testdata/wait_bad_timeout.yaml: step "w": timeout: "forever" is not a valid duration; use Go duration syntax, e.g. "5m", "1h"`,
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
	for _, file := range []string{"tidy.yaml", "valid.yaml", "parallel_ok.yaml", "human_next_valid.yaml", "human_valid_static.yaml", "human_valid_options_from.yaml", "rule15_guards_accepted.yaml", "rule15_guard_only_in_parallel_branch.yaml"} {
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
