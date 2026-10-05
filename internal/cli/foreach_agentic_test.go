package cli

import (
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/engine"
)

func itemPtr(i int) *int { return &i }

func TestFormatDispatchParallel_ForeachItems(t *testing.T) {
	d := engine.DispatchParallel{
		RunID: "b758", Step: "fan", Attempt: 1,
		Agentic: []engine.Dispatch{
			{RunID: "b758", Step: "one", Attempt: 1, Item: itemPtr(0), Description: "do a",
				WritesKeys: []string{"r"}, WritesTypes: map[string]string{"r": "string"}},
			{RunID: "b758", Step: "one", Attempt: 1, Item: itemPtr(2), Description: "do c",
				WritesKeys: []string{"r"}, WritesTypes: map[string]string{"r": "string"}},
		},
	}
	got := formatDispatchParallel(d, map[string]int{"one": 1})
	want := `DISPATCH_PARALLEL b758 fan
  DISPATCH b758 one[0]
  attempt: 1 of 1
  description:
    do a
  context: (none)
  return: a JSON object with exactly these keys (key order does not matter)
    r: string
  subagent_args: (none)
  submit with: pawl submit --run b758 --step one --item 0 --json '<the object above>'
  END DISPATCH b758 one[0]
  DISPATCH b758 one[2]
  attempt: 1 of 1
  description:
    do c
  context: (none)
  return: a JSON object with exactly these keys (key order does not matter)
    r: string
  subagent_args: (none)
  submit with: pawl submit --run b758 --step one --item 2 --json '<the object above>'
  END DISPATCH b758 one[2]
END DISPATCH_PARALLEL b758 fan
`
	if got != want {
		t.Errorf("mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatItemRecorded(t *testing.T) {
	got := formatItemRecorded(engine.ItemRecorded{RunID: "b758", ForeachStep: "fan", BodyStep: "one", Item: 2, Remaining: []int{0, 1}})
	want := "~ item one[2] recorded (foreach fan: waiting on: one[0], one[1])\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

const cliForeachAgentic = `workflow: fe
start: produce
state:
  xs: {type: json}
  out: {type: json, default: []}
  r: {type: string, default: ""}
steps:
  - id: produce
    kind: deterministic
    run: |-
      echo '{"xs":["a","b"]}'
    writes: [xs]
    next: fan
  - id: fan
    kind: parallel
    foreach: {over: xs, body: one, collect: out}
    outcomes: {success: done, partial: done, failure: done}
  - id: one
    kind: agentic
    description: "handle ${item}"
    writes: {r: {type: string}}
    postcondition: {all_set: [r]}
terminal:
  done: {status: ok}
`

func TestForeachAgentic_CLIRoundTrip(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "fe", cliForeachAgentic)
	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "fe"})
	if code != 0 {
		t.Fatalf("run: exit %d stderr %q", code, stderr)
	}
	line := firstLineHavingPrefix(t, stdout, "DISPATCH_PARALLEL ")
	runID := extractRunID(t, line)
	for _, want := range []string{"  DISPATCH " + runID + " one[0]", "  DISPATCH " + runID + " one[1]",
		"  END DISPATCH " + runID + " one[1]", "--step one --item 1 --json '<the object above>'", "handle a", "handle b"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("run output missing %q:\n%s", want, stdout)
		}
	}

	out, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", runID, "--step", "one", "--item", "1", "--json", `{"r":"x"}`})
	if code != 0 || out != "~ item one[1] recorded (foreach fan: waiting on: one[0])\n" {
		t.Fatalf("submit item 1: exit %d out %q stderr %q", code, out, stderr)
	}
	out, stderr, code = runCLI(t, []string{"pawl", "submit", "--run", runID, "--step", "one", "--item", "0", "--json", `{"r":"y"}`})
	if code != 0 || !strings.HasPrefix(out, "TERMINAL") {
		t.Fatalf("submit item 0: exit %d out %q stderr %q", code, out, stderr)
	}
}

func TestSubmit_ItemRefusals(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "fe", cliForeachAgentic)
	stdout, _, _ := runCLI(t, []string{"pawl", "run", "fe"})
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH_PARALLEL "))

	for name, args := range map[string][]string{
		"missing --item": {"--step", "one", "--json", `{"r":"x"}`},
		"out of range":   {"--step", "one", "--item", "9", "--json", `{"r":"x"}`},
		"item on other":  {"--step", "fan", "--item", "0", "--json", `{}`},
	} {
		_, stderr, code := runCLI(t, append([]string{"pawl", "submit", "--run", runID}, args...))
		if code != 4 {
			t.Errorf("%s: exit = %d, want 4; stderr %q", name, code, stderr)
		}
	}
}

func TestSubmit_ItemFlagParsing(t *testing.T) {
	setupWorkingCopy(t)
	for _, bad := range []string{"abc", "-1", "1.5", ""} {
		_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", "r", "--step", "s", "--item", bad, "--json", "{}"})
		if code != 2 || !strings.Contains(stderr, "--item") {
			t.Errorf("--item %q: exit %d stderr %q, want usage exit 2 naming --item", bad, code, stderr)
		}
	}
	_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", "r", "--step", "s", "--item"})
	if code != 2 || !strings.Contains(stderr, "--item needs a value") {
		t.Errorf("bare --item: exit %d stderr %q", code, stderr)
	}
}
