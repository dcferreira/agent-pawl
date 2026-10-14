package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/render"
)

func TestRenderValue_ByDeclaredType(t *testing.T) {
	cases := []struct {
		name     string
		declType string
		v        any
		want     string
	}{
		{"int64", "integer", int64(42), "42"},
		{"int", "integer", 7, "7"},
		{"float integral 1e6", "integer", float64(1000000), "1000000"},
		{"float integral 123456789", "integer", float64(123456789), "123456789"},
		{"float zero", "integer", float64(0), "0"},
		{"float negative", "integer", float64(-5), "-5"},
		{"json.Number integer", "integer", json.Number("1000000"), "1000000"},
		{"json.Number big integer", "integer", json.Number("9007199254740993"), "9007199254740993"},
		{"integer non-integral float", "integer", 2.5, "2.5"},
		{"number 0.1", "number", 0.1, "0.1"},
		{"number 1e-7", "number", 1e-7, "0.0000001"},
		{"number 1e21", "number", 1e21, "1000000000000000000000"},
		{"number 2.5", "number", 2.5, "2.5"},
		{"number 1e6", "number", float64(1000000), "1000000"},
		{"number int64", "number", int64(3), "3"},
		{"number json.Number 1e6", "number", json.Number("1e6"), "1000000"},
		{"number json.Number 1000000", "number", json.Number("1000000"), "1000000"},
		{"number json.Number bad", "number", json.Number("xyz"), "xyz"},
		{"string", "string", "hi", "hi"},
		{"boolean", "boolean", true, "true"},
		{"nil", "integer", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := render.RenderProse("${k}", render.Values{"k": renderValue(c.declType, c.v)})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("renderValue(%s, %#v) = %q, want %q", c.declType, c.v, got, c.want)
			}
		})
	}
}

func TestRenderValue_JSONUnchanged(t *testing.T) {
	got, err := render.RenderProse("${k}", render.Values{"k": renderValue("json", map[string]any{"a": float64(1)})})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := render.RenderProse("${k}", render.Values{"k": render.JSONValue(map[string]any{"a": float64(1)})})
	if got != want {
		t.Errorf("json: got %q want %q", got, want)
	}
}

// TestRender_NumbersSurviveJournalReplay drives a real run: step a writes
// integer/number state through emits: json, step b (a fresh replay of the
// journal, where numbers come back as float64) renders them via ${} and
// $PAWL_ env and must never see exponent form.
func TestRender_NumbersSurviveJournalReplay(t *testing.T) {
	const yaml = `
workflow: numfmt
start: a
state:
  n: {type: integer, default: 0}
  x: {type: number, default: 0}
  big: {type: integer, default: 0}
steps:
  - id: a
    kind: deterministic
    run: 'echo "{\"n\": 1000000, \"x\": 1e6, \"big\": 123456789}"'
    emits: json
    writes: [n, x, big]
    next: b
  - id: b
    kind: deterministic
    run: 'echo ${n} ${x} ${big} $PAWL_N $PAWL_X $PAWL_BIG > out.txt'
    next: done
terminal:
  done: {status: ok, message: "finished"}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("got %#v", instr)
	}
	b, err := os.ReadFile(filepath.Join(e.Root, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "1000000 1000000 123456789 1000000 1000000 123456789\n"
	if string(b) != want {
		t.Errorf("rendered %q, want %q", b, want)
	}
}
