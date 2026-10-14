package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rules 37-39 tests plus the json:"-" guards for the authoring-only fields
// (journal digests marshal these types, so a new key would change every
// existing workflow's digest).

func TestAuthoringOnlyFieldsAbsentFromJSON(t *testing.T) {
	cases := []struct {
		name  string
		value any
		field string
	}{
		{"StateDecl.DefaultSet", StateDecl{Type: "string", DefaultSet: true}, "DefaultSet"},
		{"ArgDecl.DefaultSet", ArgDecl{Type: "string", DefaultSet: true}, "DefaultSet"},
		{"CatchRule.UnknownKeys", CatchRule{On: "x", Next: "done", UnknownKeys: []string{"zzz"}}, "UnknownKeys"},
		{"Step.Fields", Step{ID: "a", Fields: []string{"id", "kind"}}, "Fields"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"`+c.field+`"`) {
				t.Errorf("json of %s contains %q: %s", c.name, c.field, b)
			}
		})
	}
}

func namesWF(extraState, extraArgs, extraSteps, terminal string) string {
	src := "workflow: n\nstart: a\n"
	if extraArgs != "" {
		src += "args:\n" + extraArgs
	}
	src += "state:\n  s: {type: string}\n" + extraState
	src += "steps:\n  - id: a\n    kind: deterministic\n    run: echo ok\n    next: done\n" + extraSteps
	if terminal != "" {
		src += "terminal:\n" + terminal
	}
	return src
}

func TestRule37_ReservedStepIDs(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"step id done", namesWF("", "", "  - id: done\n    kind: deterministic\n    run: echo\n    next: blocked\n", ""),
			[]string{`rule 37: step id "done" is reserved for the implicit terminal; rename the step`,
				`rule 37: step id "done"`}},
		{"step id blocked", namesWF("", "", "  - id: blocked\n    kind: deterministic\n    run: echo\n    next: done\n", ""),
			[]string{`rule 37: step id "blocked" is reserved for the implicit terminal; rename the step`}},
		{"step collides with terminal", namesWF("", "", "  - id: fin\n    kind: deterministic\n    run: echo\n    next: done\n", "  fin: {status: ok}\n"),
			[]string{`rule 37: step id "fin" collides with terminal: "fin"; rename one`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { expectErrs(t, tc.src, tc.want...) })
	}
	t.Run("explicit terminal done/blocked stays valid", func(t *testing.T) {
		expectClean(t, namesWF("", "", "", "  done: {status: ok, message: fine}\n  blocked: {status: blocked, message: stuck}\n"))
	})
}

func TestRule38_ReservedKeyNames(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"state attempt", namesWF("  attempt: {type: string}\n", "", "", ""),
			[]string{`rule 38: state: "attempt" is an engine pseudo-key name (exported as PAWL_ATTEMPT); rename it`}},
		{"state run_id", namesWF("  run_id: {type: string}\n", "", "", ""),
			[]string{`rule 38: state: "run_id" is an engine pseudo-key name (exported as PAWL_RUN_ID); rename it`}},
		{"case-insensitive Attempt", namesWF("  Attempt: {type: string}\n", "", "", ""),
			[]string{`rule 38: state: "Attempt" is an engine pseudo-key name (exported as PAWL_ATTEMPT); rename it`}},
		{"args last_error", namesWF("", "  last_error: {type: string}\n", "", ""),
			[]string{`rule 38: args: "last_error" is an engine pseudo-key name (exported as PAWL_LAST_ERROR); rename it`}},
		{"state item (folded from rule 27)", namesWF("  item: {type: string}\n", "", "", ""),
			[]string{`rule 38: state: "item" is an engine pseudo-key name`, `!rule 27: state:`}},
		{"leading underscore", namesWF("  _x: {type: string}\n", "", "", ""),
			[]string{`rule 38: state: "_x" starts with "_"; keys exported as PAWL__* are reserved for pawl's own future environment variables; rename it`}},
		{"enforcement", namesWF("  enforcement: {type: string}\n", "", "", ""),
			[]string{`rule 38: state: "enforcement" would export as PAWL_ENFORCEMENT, an environment variable pawl itself reads; rename it`}},
		{"state_dir", namesWF("", "  state_dir: {type: string}\n", "", ""),
			[]string{`rule 38: args: "state_dir" would export as PAWL_STATE_DIR, an environment variable pawl itself reads; rename it`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { expectErrs(t, tc.src, tc.want...) })
	}
	t.Run("ordinary keys are fine", func(t *testing.T) {
		expectClean(t, namesWF("  attempts_left: {type: string}\n  my_item: {type: string}\n", "", "", ""))
	})
	t.Run("exported reserved env list", func(t *testing.T) {
		if len(ReservedEnvNames) != 2 || ReservedEnvNames[0] != "PAWL_ENFORCEMENT" || ReservedEnvNames[1] != "PAWL_STATE_DIR" {
			t.Errorf("ReservedEnvNames = %v", ReservedEnvNames)
		}
	})
}

func TestRule39_KeyCollisions(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"same key in args and state", namesWF("  k: {type: string}\n", "  k: {type: string}\n", "", ""),
			[]string{`rule 39: "k" is declared in both args: and state:; keys share one namespace; rename one`}},
		{"state Foo and foo", namesWF("  Foo: {type: string}\n  foo: {type: string}\n", "", "", ""),
			[]string{`rule 39: state: "Foo" and state: "foo" both export as PAWL_FOO; rename one`}},
		{"args Foo and state foo", namesWF("  foo: {type: string}\n", "  Foo: {type: string}\n", "", ""),
			[]string{`rule 39: args: "Foo" and state: "foo" both export as PAWL_FOO; rename one`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { expectErrs(t, tc.src, tc.want...) })
	}
	t.Run("deterministic order", func(t *testing.T) {
		src := namesWF("  Foo: {type: string}\n  foo: {type: string}\n  Bar: {type: string}\n  bar: {type: string}\n", "", "", "")
		first := strings.Join(validateYAML(t, src), "\n")
		for i := 0; i < 20; i++ {
			if got := strings.Join(validateYAML(t, src), "\n"); got != first {
				t.Fatalf("order changed:\n%s\n---\n%s", first, got)
			}
		}
		if strings.Index(first, `"Bar"`) > strings.Index(first, `"Foo"`) {
			t.Errorf("expected Bar collision before Foo:\n%s", first)
		}
	})
}
