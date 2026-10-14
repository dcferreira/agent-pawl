package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

func coerceWorkflow(declType string) string {
	return "workflow: co\nstart: work\nstate:\n  k: {type: " + declType + "}\nsteps:\n  - id: work\n    kind: agentic\n    description: d\n    writes: {k: {type: " + declType + "}}\n    postcondition: \"true\"\n    attempts: 3\n    next: done\nterminal: {done: {status: ok, message: \"k=${k}\"}}\n"
}

// TestSubmit_IntegerRejectsNonIntegral: a non-integral or exponent-form
// number submitted to an integer key is a typed-write rejection exactly like
// max_length: a failed attempt carrying the reason, never an engine error.
func TestSubmit_IntegerRejectsNonIntegral(t *testing.T) {
	for _, bad := range []string{"3.7", "1e2", "99999999999999999999"} {
		t.Run(bad, func(t *testing.T) {
			e := newTestEngine(t, coerceWorkflow("integer"))
			if _, err := e.Start("run1", nil); err != nil {
				t.Fatalf("Start: %v", err)
			}
			instr, err := e.Submit("run1", "work", 1, []byte(`{"k":`+bad+`}`))
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			d, ok := instr.(Dispatch)
			if !ok || d.Attempt != 2 {
				t.Fatalf("got %+v, want Dispatch attempt 2", instr)
			}
			for _, want := range []string{`step "work"`, `key "k"`, `"integer"`} {
				if !strings.Contains(d.PreviousFailure, want) {
					t.Errorf("PreviousFailure = %q, want it to contain %q", d.PreviousFailure, want)
				}
			}
		})
	}
}

// TestCoerceAgenticValue_IntegerKeepsFullPrecision: an integer above 2^53 is
// coerced exactly, not rounded through float64. (The journal's own replay
// still decodes numbers as float64; that is outside submit's coercion.)
func TestCoerceAgenticValue_IntegerKeepsFullPrecision(t *testing.T) {
	got, err := coerceAgenticValue("k", json.Number("9007199254740993"), "integer")
	if err != nil || got != int64(9007199254740993) {
		t.Fatalf("got %#v, %v; want int64(9007199254740993)", got, err)
	}
}

// TestSubmit_StringFromJSONNumberKeepsOwnText: a JSON number submitted to a
// string key is stored as its own text, not a float64 re-rendering.
func TestSubmit_StringFromJSONNumberKeepsOwnText(t *testing.T) {
	for _, lit := range []string{"1000000", "1e6", "1.50"} {
		t.Run(lit, func(t *testing.T) {
			e := newTestEngine(t, coerceWorkflow("string"))
			if _, err := e.Start("run1", nil); err != nil {
				t.Fatalf("Start: %v", err)
			}
			instr, err := e.Submit("run1", "work", 1, []byte(`{"k":`+lit+`}`))
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			term, ok := instr.(Terminal)
			if !ok || term.Message != "k="+lit {
				t.Fatalf("got %+v, want Terminal with k=%s", instr, lit)
			}
		})
	}
}

// TestSubmit_JSONValueKeepsNumbers: a json-typed key containing numbers still
// works after submit decodes with UseNumber.
func TestSubmit_JSONValueKeepsNumbers(t *testing.T) {
	e := newTestEngine(t, coerceWorkflow("json"))
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	instr, err := e.Submit("run1", "work", 1, []byte(`{"k":{"n":[1,2.5]}}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal ok", instr)
	}
}

// TestSubmit_NumberAndBooleanCoercion: number accepts a JSON number; the
// agentic boolean is frozen at JSON bool only (no ParseBool strings).
func TestSubmit_NumberAndBooleanCoercion(t *testing.T) {
	e := newTestEngine(t, coerceWorkflow("number"))
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if instr, err := e.Submit("run1", "work", 1, []byte(`{"k":2.5}`)); err != nil {
		t.Fatalf("Submit: %v", err)
	} else if term, ok := instr.(Terminal); !ok || term.Message != "k=2.5" {
		t.Fatalf("number: got %+v", instr)
	}

	e = newTestEngine(t, coerceWorkflow("boolean"))
	if _, err := e.Start("run1", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	instr, err := e.Submit("run1", "work", 1, []byte(`{"k":"true"}`))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if d, ok := instr.(Dispatch); !ok || d.Attempt != 2 {
		t.Fatalf("boolean string: got %+v, want rejection (Dispatch attempt 2)", instr)
	}
}

func TestDecodeSubmitted_TrailingData(t *testing.T) {
	bad := []string{`{"a":1}]`, `{"a":1}}`, `{"a":1} {"b":2}`, `{"a":1}x`}
	for _, s := range bad {
		var m map[string]any
		if err := decodeSubmitted([]byte(s), &m); err == nil {
			t.Errorf("%q: accepted, want error", s)
		}
	}
	for _, s := range []string{"{\"a\":1}\n", "{\"a\":1}  \t\n"} {
		var m map[string]any
		if err := decodeSubmitted([]byte(s), &m); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
}
