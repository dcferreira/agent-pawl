package spec

import (
	"strings"
	"testing"
)

// Rules 30-36 tests. Every workflow here goes through YAML (validateYAML in
// foreach_test.go): rule 30 works on Step.Fields, which only Step.UnmarshalYAML
// populates, so a Step struct literal built directly in Go has no authored
// keys and rule 30 sees nothing to flag (acceptable: Load is the only
// production path).

const kindsHead = `workflow: k
start: a
`

// kindsOK is a minimal valid workflow every case below mutates or replaces.
const kindsOK = kindsHead + `state:
  s: {type: string}
steps:
  - id: a
    kind: deterministic
    run: echo ok
    next: done
`

// expectErrs asserts that, for each want substring, at least one validate
// error contains it, and that no error contains any of the forbidden
// substrings (prefixed with "!").
func expectErrs(t *testing.T, src string, want ...string) {
	t.Helper()
	got := validateYAML(t, src)
	joined := strings.Join(got, "\n")
	for _, w := range want {
		if strings.HasPrefix(w, "!") {
			if strings.Contains(joined, w[1:]) {
				t.Errorf("errors unexpectedly contain %q:\n%s", w[1:], joined)
			}
			continue
		}
		if !strings.Contains(joined, w) {
			t.Errorf("errors missing %q; got:\n%s", w, joined)
		}
	}
}

func expectClean(t *testing.T, src string) {
	t.Helper()
	if got := validateYAML(t, src); len(got) != 0 {
		t.Fatalf("expected zero errors, got:\n%s", strings.Join(got, "\n"))
	}
}

func TestRule30_PerKindFields(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "question/poll/every on deterministic",
			src: kindsHead + `steps:
  - id: a
    kind: deterministic
    run: echo ok
    question: "why?"
    poll: "true"
    every: 5s
    next: done
`,
			want: []string{
				"rule 30: kind: deterministic does not accept every:, poll:, question:",
				"belong to kind: wait",
				"belongs to kind: human",
				"remove",
			},
		},
		{
			name: "attempts on wait",
			src: kindsHead + `steps:
  - id: a
    kind: wait
    poll: "true"
    timeout: 5m
    attempts: 3
    outcomes: {success: done, timeout: done}
`,
			want: []string{"rule 30: kind: wait does not accept attempts:", "remove"},
		},
		{
			name: "multi false on deterministic is still flagged",
			src: kindsHead + `steps:
  - id: a
    kind: deterministic
    run: echo ok
    multi: false
    next: done
`,
			want: []string{"rule 30: kind: deterministic does not accept multi:", "!rule 8: multi"},
		},
		{
			name: "foreach on deterministic",
			src: kindsHead + `steps:
  - id: a
    kind: deterministic
    run: echo ok
    foreach: {over: x, body: b, collect: y}
    next: done
`,
			want: []string{"rule 30: kind: deterministic does not accept foreach:"},
		},
		{
			name: "retry on agentic",
			src: kindsHead + `state:
  s: {type: string}
steps:
  - id: a
    kind: agentic
    description: do it
    writes: {s: {type: string}}
    postcondition: {command: "true"}
    retry: {max_attempts: 2, backoff: 1s}
    next: done
`,
			want: []string{"rule 30: kind: agentic does not accept retry:"},
		},
		{
			name: "unknown key is not double-reported by rule 30",
			src: kindsHead + `steps:
  - id: a
    kind: deterministic
    run: echo ok
    nxt: done
    next: done
`,
			want: []string{`has unknown field "nxt"`, "!rule 30"},
		},
		{
			name: "unknown kind gets no rule 30",
			src: kindsHead + `steps:
  - id: a
    kind: determinstic
    run: echo ok
    next: done
`,
			want: []string{`kind "determinstic" is not one of`, "!rule 30"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { expectErrs(t, tt.src, tt.want...) })
	}
}

func TestRule30_ValidKindFieldsPass(t *testing.T) {
	expectClean(t, kindsOK)
}

func TestRule31_SoftRequiresPostcondition(t *testing.T) {
	expectErrs(t, kindsHead+`steps:
  - id: a
    kind: deterministic
    run: echo ok
    soft: true
    next: done
`, "rule 31: soft: requires postcondition:")

	// soft: false is still an authored soft: key.
	expectErrs(t, kindsHead+`steps:
  - id: a
    kind: deterministic
    run: echo ok
    soft: false
    next: done
`, "rule 31")

	// On human, soft: is a rule 30 fault only; do not double-report.
	expectErrs(t, kindsHead+`state:
  s: {type: string}
steps:
  - id: a
    kind: human
    question: q
    options: [x]
    timeout: 1h
    soft: true
    outcomes: {x: done, timeout: done}
`, "rule 30: kind: human does not accept soft:", "!rule 31")

	expectClean(t, kindsHead+`steps:
  - id: a
    kind: deterministic
    run: echo ok
    soft: true
    postcondition: {command: "true"}
    next: done
`)
}

func TestRule32_TypeEnum(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"typo strnig", kindsHead + "state:\n  x: {type: strnig}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n",
			[]string{`rule 32: state: "x": type: "strnig" is not one of string, integer, number, boolean, json (did you mean "string"?)`}},
		{"typo integr", kindsHead + "state:\n  x: {type: integr}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n",
			[]string{"rule 32", `"integr"`, `did you mean "integer"?`}},
		{"missing type on state", kindsHead + "state:\n  x: {}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n",
			[]string{`rule 32: state: "x" has no type:; add type: string|integer|number|boolean|json`}},
		{"missing type on arg", kindsHead + "args:\n  x: {required: true}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n",
			[]string{`rule 32: args: "x" has no type:`}},
		{"bad arg type", kindsHead + "args:\n  x: {type: str}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n",
			[]string{`rule 32: args: "x": type: "str"`}},
		{"bad writes type", kindsHead + "state:\n  s: {type: string}\nsteps:\n  - id: a\n    kind: agentic\n    description: d\n    writes: {s: {type: strin}}\n    postcondition: {command: \"true\"}\n    next: done\n",
			[]string{`rule 32: writes: "s": type: "strin"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { expectErrs(t, tt.src, tt.want...) })
	}
	for _, ty := range []string{"string", "integer", "number", "boolean", "json"} {
		expectClean(t, kindsHead+"state:\n  x: {type: "+ty+"}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n")
	}
}

func TestRule33_DefaultMatchesType(t *testing.T) {
	bad := []struct{ name, typ, def, want string }{
		{"int on string", "string", "5", `rule 33: state: "z": default: 5 is an integer, but type: is string; quote it ("5") or change the type`},
		{"string on integer", "integer", `"5"`, "rule 33"},
		{"float on integer", "integer", "1.5", "rule 33"},
		{"bool on integer", "integer", "true", "rule 33"},
		{"string on number", "number", `"1.5"`, "rule 33"},
		{"string on boolean", "boolean", `"true"`, "rule 33"},
		{"int on boolean", "boolean", "1", "rule 33"},
		{"null on string", "string", "null", "rule 33"},
		{"null on integer", "integer", "null", "rule 33"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			expectErrs(t, kindsHead+"state:\n  z: {type: "+tt.typ+", default: "+tt.def+"}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n", tt.want)
		})
	}
	t.Run("arg default checked too", func(t *testing.T) {
		expectErrs(t, kindsHead+"args:\n  z: {type: string, default: 5}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n", `rule 33: args: "z"`)
	})
	t.Run("bad type skips rule 33", func(t *testing.T) {
		expectErrs(t, kindsHead+"state:\n  z: {type: strnig, default: 5}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n", "rule 32", "!rule 33")
	})
	good := []struct{ typ, def string }{
		{"string", `"5"`}, {"integer", "5"}, {"number", "1.5"}, {"number", "2"},
		{"boolean", "true"}, {"json", "null"}, {"json", "[1, 2]"}, {"json", "5"},
	}
	for _, tt := range good {
		t.Run("ok "+tt.typ+" "+tt.def, func(t *testing.T) {
			expectClean(t, kindsHead+"state:\n  z: {type: "+tt.typ+", default: "+tt.def+"}\nsteps:\n  - {id: a, kind: deterministic, run: echo, next: done}\n")
		})
	}
}

func TestRule34_StrictCatchEntries(t *testing.T) {
	tests := []struct {
		name  string
		catch string
		want  []string
	}{
		{"unknown key nxt", "[{on: failure, next: done, nxt: zzz}]",
			[]string{`rule 34: catch[0] has unknown key "nxt" (did you mean "next"?); use only on: and next:`}},
		{"missing on", "[{next: done}]", []string{"rule 34: catch[0].on: is required"}},
		{"empty on", `[{on: "", next: done}]`, []string{"rule 34: catch[0].on: is required"}},
		{"missing next stays rule 1", "[{on: failure}]", []string{"rule 1: catch"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectErrs(t, kindsHead+"steps:\n  - {id: a, kind: deterministic, run: echo, next: done, catch: "+tt.catch+"}\n", tt.want...)
		})
	}
}

func TestRule30_AttemptKeyIsCommon(t *testing.T) {
	t.Run("attempt_key on wait", func(t *testing.T) {
		expectClean(t, kindsHead+"steps:\n  - id: a\n    kind: wait\n    poll: \"true\"\n    timeout: 5m\n    attempt_key: \"k\"\n    outcomes: {success: done, timeout: done}\n")
	})
	t.Run("attempt_key on human", func(t *testing.T) {
		expectClean(t, kindsHead+"steps:\n  - id: a\n    kind: human\n    question: q\n    options: [x]\n    timeout: 1h\n    attempt_key: \"k\"\n    next: done\n")
	})
	t.Run("attempt_key on parallel", func(t *testing.T) {
		expectClean(t, kindsHead+`steps:
  - {id: a, kind: parallel, branches: [b, c], attempt_key: "k", outcomes: {success: done, failure: blocked}}
  - {id: b, kind: deterministic, run: echo}
  - {id: c, kind: deterministic, run: echo}
`)
	})
	t.Run("attempts on parallel still rejected", func(t *testing.T) {
		expectErrs(t, kindsHead+`steps:
  - {id: a, kind: parallel, branches: [b, c], attempts: 2, outcomes: {success: done, failure: blocked}}
  - {id: b, kind: deterministic, run: echo}
  - {id: c, kind: deterministic, run: echo}
`, "rule 30: kind: parallel does not accept attempts:")
	})
}

func TestRule35_CatchOn(t *testing.T) {
	det := func(catch string) string {
		return kindsHead + "steps:\n  - {id: a, kind: deterministic, run: echo, next: done, catch: " + catch + "}\n"
	}
	t.Run("typo failur", func(t *testing.T) {
		expectErrs(t, det("[{on: failur, next: done}]"),
			`rule 35: catch[0].on: "failur" is not an outcome a kind: deterministic step can catch (failure, exhausted); route author-named outcomes under outcomes:`)
	})
	t.Run("timeout on deterministic", func(t *testing.T) {
		expectErrs(t, det("[{on: timeout, next: done}]"), "rule 35")
	})
	t.Run("success not catchable", func(t *testing.T) {
		expectErrs(t, det("[{on: success, next: done}]"), "rule 35")
	})
	t.Run("duplicate on", func(t *testing.T) {
		expectErrs(t, det("[{on: failure, next: done}, {on: failure, next: blocked}]"),
			`rule 35: catch[1].on: "failure" duplicates catch[0]; the later entry can never fire, remove it`)
	})
	t.Run("shadowed by outcomes", func(t *testing.T) {
		expectErrs(t, kindsHead+"steps:\n  - {id: a, kind: deterministic, run: echo, outcomes: {ok: done, failure: done}, catch: [{on: failure, next: blocked}]}\n",
			`rule 35: catch[0].on: "failure" is already routed by outcomes:, which takes precedence; remove one`)
	})
	t.Run("catch-only outcome token on deterministic", func(t *testing.T) {
		// emit.Parse would reject "bad" (named only by catch:) before routing, so
		// the validator rejects the shape up front.
		expectErrs(t, kindsHead+`steps:
  - id: a
    kind: deterministic
    run: echo bad
    outcomes: {good: done}
    catch: [{on: bad, next: fallback}]
  - {id: fallback, kind: deterministic, run: "true", next: done}
`, "rule 35")
	})
	t.Run("wait timeout ok", func(t *testing.T) {
		expectClean(t, kindsHead+"steps:\n  - id: a\n    kind: wait\n    poll: \"true\"\n    timeout: 5m\n    next: done\n    catch: [{on: timeout, next: done}]\n")
	})
	t.Run("human timeout ok", func(t *testing.T) {
		expectClean(t, kindsHead+"steps:\n  - id: a\n    kind: human\n    question: q\n    options: [x]\n    timeout: 1h\n    next: done\n    catch: [{on: timeout, next: done}]\n")
	})
	t.Run("foreach partial ok", func(t *testing.T) {
		expectClean(t, strings.Replace(foreachBase,
			"outcomes: {success: done, partial: done, failure: blocked}",
			"outcomes: {success: done, failure: blocked}\n    catch: [{on: partial, next: done}]", 1))
	})
	t.Run("branches parallel cannot catch partial", func(t *testing.T) {
		expectErrs(t, kindsHead+`steps:
  - {id: a, kind: parallel, branches: [b, c], outcomes: {success: done, failure: blocked}, catch: [{on: partial, next: done}]}
  - {id: b, kind: deterministic, run: echo}
  - {id: c, kind: deterministic, run: echo}
`, "rule 35: catch[0].on: \"partial\"")
	})
}

func TestRule36_Durations(t *testing.T) {
	wait := func(extra string) string {
		return kindsHead + "steps:\n  - id: a\n    kind: wait\n    poll: \"true\"\n" + extra + "    outcomes: {success: done, timeout: done}\n"
	}
	human := func(timeout string) string {
		return kindsHead + "steps:\n  - id: a\n    kind: human\n    question: q\n    options: [x]\n    timeout: \"" + timeout + "\"\n    outcomes: {x: done, timeout: done}\n"
	}
	t.Run("human 3 days", func(t *testing.T) {
		expectErrs(t, human("3 days"), `rule 36: timeout: "3 days" is not a valid duration; use Go duration syntax, e.g. "5m", "1h", "24h"`)
	})
	t.Run("human zero", func(t *testing.T) {
		expectErrs(t, human("0s"), `rule 36: timeout: "0s" must be greater than zero`)
	})
	t.Run("human negative", func(t *testing.T) {
		expectErrs(t, human("-5m"), "rule 36")
	})
	t.Run("wait bad timeout", func(t *testing.T) {
		expectErrs(t, wait("    timeout: forever\n"), `rule 36: timeout: "forever" is not a valid duration`)
	})
	t.Run("wait zero timeout", func(t *testing.T) {
		expectErrs(t, wait("    timeout: 0s\n"), "rule 36: timeout: \"0s\" must be greater than zero")
	})
	t.Run("wait bad every", func(t *testing.T) {
		expectErrs(t, wait("    timeout: 5m\n    every: soon\n"), `rule 36: every: "soon" is not a valid duration`)
	})
	t.Run("wait zero every", func(t *testing.T) {
		expectErrs(t, wait("    timeout: 5m\n    every: 0s\n"), "rule 36: every: \"0s\" must be greater than zero")
	})
	t.Run("valid", func(t *testing.T) {
		expectClean(t, wait("    timeout: 24h\n    every: 30s\n"))
		expectClean(t, human("1h"))
	})
}
