package engine

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/render"
)

func TestIsEmptyValue_Numbers(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{json.Number("0"), true},
		{json.Number("0.0"), true},
		{json.Number("-0"), true},
		{json.Number("0e5"), true},
		{json.Number("1"), false},
		{json.Number("0.5"), false},
		{json.Number("9007199254740993"), false},
		{json.Number("1e999999999"), false},
		{0, true},
		{7, false},
		{uint64(0), true},
		{uint64(3), false},
		{int64(0), true},
		{float64(0), true},
		{float64(2), false},
	}
	for _, c := range cases {
		if got := isEmptyValue(c.v); got != c.want {
			t.Errorf("isEmptyValue(%#v) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestEvaluateEquals_NumericPairings(t *testing.T) {
	cases := []struct {
		name     string
		want, in any
		match    bool
	}{
		{"int vs json.Number", 1000000, json.Number("1000000"), true},
		{"int vs json.Number exponent", 1000000, json.Number("1e6"), true},
		{"int vs json.Number decimal", 1000000, json.Number("1000000.0"), true},
		{"int64 vs int64", int64(5), int64(5), true},
		{"uint64 vs json.Number", uint64(5), json.Number("5"), true},
		{"float vs json.Number", float64(2.5), json.Number("2.5"), true},
		{"float integral vs json.Number", float64(1000000), json.Number("1000000"), true},
		{"int vs float64", 1000000, float64(1000000), true},
		{"big int exact", 9007199254740993, json.Number("9007199254740993"), true},
		{"big int off by one", 9007199254740993, json.Number("9007199254740992"), false},
		{"int mismatch", 1, json.Number("2"), false},
		{"zero vs negative zero", 0, json.Number("-0"), true},
		{"yaml string vs json.Number match", "1000000", json.Number("1000000"), true},
		{"yaml string vs json.Number mismatch", "1000000", json.Number("1000001"), false},
		{"yaml string exponent vs json.Number stays textual", "1000000", json.Number("1e6"), false},
		{"string vs string", "ready", "ready", true},
		{"string vs string mismatch", "ready", "nope", false},
		{"bool vs bool", true, true, true},
		{"bool mismatch", true, false, false},
		{"int vs string actual", 5, "5", true},
	}
	for _, c := range cases {
		res := evaluateEquals(map[string]any{"n": c.want}, map[string]any{"n": c.in}, render.Values{}, false)
		if res.OK != c.match {
			t.Errorf("%s: equals %#v vs %#v: OK=%v (%s), want %v", c.name, c.want, c.in, res.OK, res.Text, c.match)
		}
	}
	// A missing key still compares as empty text.
	if res := evaluateEquals(map[string]any{"n": 0}, map[string]any{}, render.Values{}, false); res.OK {
		t.Error("missing key matched equals: 0")
	}
}

// numParityWorkflow writes n (from a JSON literal) in step a, then step chk
// checks post. A failed postcondition blocks the run (attempts: 1, no catch).
func numParityWorkflow(lit, post string) string {
	return `
workflow: numparity
start: a
state:
  n: {type: integer}
steps:
  - id: a
    kind: deterministic
    run: |-
      echo '{"n":` + lit + `}'
    writes: [n]
    next: chk
  - id: chk
    kind: deterministic
    run: "true"
    postcondition: ` + post + `
    attempts: 1
    next: done
terminal: {done: {status: ok}}
`
}

// verdict runs the workflow straight through (fresh) and again with the chk
// step re-driven by Resume off a replayed journal, returning both terminal
// statuses.
func parityVerdicts(t *testing.T, yaml string) (fresh, replayed string) {
	t.Helper()
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok {
		t.Fatalf("got %T %+v, want Terminal", instr, instr)
	}
	fresh = term.Status
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	truncateJournalBefore(t, dir, func(ev journal.Event) bool {
		return ev.Kind == journal.KindStepEnter && ev.Step == "chk"
	})
	instr, err = e.Resume("run1", true)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	term, ok = instr.(Terminal)
	if !ok {
		t.Fatalf("resume: got %T %+v, want Terminal", instr, instr)
	}
	return fresh, term.Status
}

func TestNumericParity_FreshVsReplayed(t *testing.T) {
	cases := []struct {
		name, lit, post, want string
	}{
		{"equals 1000000", "1000000", `{equals: {n: 1000000}}`, "ok"},
		{"equals 1000000 mismatch", "1000001", `{equals: {n: 1000000}}`, "blocked"},
		{"equals string 1000000", "1000000", `{equals: {n: "1000000"}}`, "ok"},
		{"equals big", "9007199254740993", `{equals: {n: 9007199254740993}}`, "ok"},
		{"equals big off by one", "9007199254740992", `{equals: {n: 9007199254740993}}`, "blocked"},
		{"equals zero", "0", `{equals: {n: 0}}`, "ok"},
		{"all_set zero fails", "0", `{all_set: [n]}`, "blocked"},
		{"all_set one passes", "1", `{all_set: [n]}`, "ok"},
		{"all_set 1000000 passes", "1000000", `{all_set: [n]}`, "ok"},
		{"all_set big passes", "9007199254740993", `{all_set: [n]}`, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fresh, replayed := parityVerdicts(t, numParityWorkflow(c.lit, c.post))
			if fresh != replayed {
				t.Errorf("fresh verdict %q != replayed verdict %q", fresh, replayed)
			}
			if fresh != c.want {
				t.Errorf("verdict %q, want %q", fresh, c.want)
			}
		})
	}
}

// Args handed to Start (int64 from coerceArgValue) and the same value after
// replay (json.Number) must give the same verdict.
func TestNumericParity_ArgsFreshVsReplayed(t *testing.T) {
	const yaml = `
workflow: argparity
start: pre
args:
  n: {type: integer}
steps:
  - id: pre
    kind: deterministic
    run: "true"
    next: chk
  - id: chk
    kind: deterministic
    run: "true"
    postcondition: {equals: {n: 9007199254740993}}
    attempts: 1
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	instr, err := e.Start("run1", map[string]any{"n": int64(9007199254740993)})
	if err != nil {
		t.Fatal(err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("fresh: got %+v, want ok", instr)
	}
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
	truncateJournalBefore(t, dir, func(ev journal.Event) bool {
		return ev.Kind == journal.KindStepEnter && ev.Step == "chk"
	})
	instr, err = e.Resume("run1", true)
	if err != nil {
		t.Fatal(err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "ok" {
		t.Fatalf("replayed: got %+v, want ok", instr)
	}
}

// A foreach item carrying a big int reaches the body's rendering and the
// collect: value exactly, including after a crash/resume replay.
func TestForeach_BigIntItemExactAfterReplay(t *testing.T) {
	const big = "9007199254740993"
	body := `    run: |-
      printf %s ${item} > item.txt
      printf '{"res":"%s"}' ${item}
    writes: [res]
    postcondition: {all_set: [res]}`
	e, instr, dir := feRun(t, feWorkflow(`[`+big+`]`, body, "", ""))
	wantTerminal(t, instr, "ok")
	truncateJournalBefore(t, dir, func(ev journal.Event) bool {
		return ev.Kind == journal.KindStepEnter && ev.Group == "fan"
	})
	instr, err := e.Resume("run1", true)
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "ok")
	data, err := os.ReadFile(e.Root + "/item.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != big {
		t.Errorf("rendered item = %q, want %q", data, big)
	}
	got := collectOf(t, dir)
	if item := got[0].(map[string]any)["item"]; item != json.Number(big) {
		t.Errorf("collect item = %#v, want json.Number(%s)", item, big)
	}
}
