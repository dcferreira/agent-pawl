package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// feWorkflow builds a foreach workflow: produce writes xs (the list given as
// a JSON literal), fan fans out over it running body "one", and the three
// join outcomes land on distinct terminals. bodyYAML is the body step's
// fields after its kind line; extra is spliced in at the top level (e.g.
// invariants:).
func feWorkflow(listJSON, bodyYAML, fanExtra, extra string) string {
	return `
workflow: fe
start: produce
state:
  xs: {type: json}
  out: {type: json, default: []}
  res: {type: string, default: ""}
` + extra + `
steps:
  - id: produce
    kind: deterministic
    run: |-
      echo '{"xs":` + strings.ReplaceAll(listJSON, "'", `'\''`) + `}'
    writes: [xs]
    next: fan
  - id: fan
    kind: parallel
    foreach: {over: xs, body: one, collect: out` + fanExtra + `}
    outcomes: {success: ok, partial: part, failure: bad}
  - id: one
    kind: deterministic
` + bodyYAML + `
terminal:
  ok: {status: ok}
  part: {status: ok}
  bad: {status: ok}
`
}

const okBody = `    run: |-
      printf '{"res":"%s"}' ${item}
    writes: [res]
    postcondition: {all_set: [res]}`

// failOnB fails (exit 1) for item "b".
const failOnB = `    run: |-
      test ${item} != b && printf '{"res":"%s"}' ${item}
    writes: [res]
    postcondition: {all_set: [res]}`

func feRun(t *testing.T, yaml string) (*Engine, Instruction, string) {
	t.Helper()
	e := newTestEngine(t, yaml)
	e.Sleep = func(time.Duration) {}
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return e, instr, journal.RunDir(e.Root, e.Workflow.Workflow, "run1")
}

func wantTerminal(t *testing.T, instr Instruction, step string) {
	t.Helper()
	term, ok := instr.(Terminal)
	if !ok || term.StepID != step {
		t.Fatalf("got %T %+v, want Terminal at %q", instr, instr, step)
	}
}

func collectOf(t *testing.T, dir string) []any {
	t.Helper()
	rs := mustReplay(t, dir)
	out, ok := rs.State["out"].([]any)
	if !ok {
		t.Fatalf("State[out] = %#v, want a JSON array", rs.State["out"])
	}
	return out
}

func entry(i int, item any, outcome string, writes map[string]any, errText any) map[string]any {
	if writes == nil {
		writes = map[string]any{}
	}
	return map[string]any{"index": float64(i), "item": item, "outcome": outcome, "writes": writes, "error": errText}
}

func TestForeach_AllSuccess(t *testing.T) {
	_, instr, dir := feRun(t, feWorkflow(`["a","b","c"]`, okBody, "", ""))
	wantTerminal(t, instr, "ok")
	want := []any{
		entry(0, "a", "success", map[string]any{"res": "a"}, nil),
		entry(1, "b", "success", map[string]any{"res": "b"}, nil),
		entry(2, "c", "success", map[string]any{"res": "c"}, nil),
	}
	if got := collectOf(t, dir); !reflect.DeepEqual(got, want) {
		t.Errorf("collect = %#v\nwant %#v", got, want)
	}
	rs := mustReplay(t, dir)
	if _, leaked := rs.State["res"]; leaked {
		t.Errorf("body write key res reached global state: %v", rs.State["res"])
	}
}

func TestForeach_MixedIsPartial(t *testing.T) {
	_, instr, dir := feRun(t, feWorkflow(`["a","b","c"]`, failOnB, "", ""))
	wantTerminal(t, instr, "part")
	got := collectOf(t, dir)
	if len(got) != 3 {
		t.Fatalf("collect has %d entries, want 3", len(got))
	}
	for i, wantOutcome := range []string{"success", "failure", "success"} {
		m := got[i].(map[string]any)
		if m["outcome"] != wantOutcome || m["index"] != float64(i) {
			t.Errorf("entry %d = %v, want outcome %s", i, m, wantOutcome)
		}
	}
	if got[1].(map[string]any)["error"] == nil {
		t.Errorf("failed item's error is null, want its diagnostic text (entry %v)", got[1])
	}
	if got[0].(map[string]any)["error"] != nil {
		t.Errorf("succeeded item's error = %v, want null", got[0].(map[string]any)["error"])
	}
}

func TestForeach_AllFailIsFailure(t *testing.T) {
	body := `    run: "exit 1"`
	_, instr, dir := feRun(t, feWorkflow(`["a","b"]`, body, "", ""))
	wantTerminal(t, instr, "bad")
	for i, c := range collectOf(t, dir) {
		if c.(map[string]any)["outcome"] != "failure" {
			t.Errorf("entry %d = %v, want failure", i, c)
		}
	}
}

func TestForeach_PostconditionFailureIsItemFailure(t *testing.T) {
	body := `    run: |-
      printf '{"res":"%s"}' ${item}
    writes: [res]
    postcondition: {equals: {res: a}}`
	_, instr, dir := feRun(t, feWorkflow(`["a","b"]`, body, "", ""))
	wantTerminal(t, instr, "part")
	got := collectOf(t, dir)
	if got[1].(map[string]any)["outcome"] != "failure" || got[1].(map[string]any)["error"] == nil {
		t.Errorf("entry 1 = %v, want failure with postcondition text", got[1])
	}
	// The failed item's own writes are still captured.
	if got[1].(map[string]any)["writes"].(map[string]any)["res"] != "b" {
		t.Errorf("entry 1 writes = %v, want res=b", got[1])
	}
}

func TestForeach_EmptyListIsSuccess(t *testing.T) {
	_, instr, dir := feRun(t, feWorkflow(`[]`, okBody, "", ""))
	wantTerminal(t, instr, "ok")
	got := collectOf(t, dir)
	if len(got) != 0 {
		t.Errorf("collect = %v, want []", got)
	}
}

func TestForeach_NonArrayOverIsFailure(t *testing.T) {
	for name, lit := range map[string]string{"object": `{"k":1}`, "scalar": `5`, "null": `null`} {
		t.Run(name, func(t *testing.T) {
			_, instr, dir := feRun(t, feWorkflow(lit, okBody, "", ""))
			wantTerminal(t, instr, "bad")
			rs := mustReplay(t, dir)
			if !strings.Contains(rs.LastError, "xs") || !strings.Contains(rs.LastError, "array") {
				t.Errorf("LastError = %q, want a diagnostic naming xs and array", rs.LastError)
			}
		})
	}
}

func TestForeach_OverMaxItemsIsExhausted(t *testing.T) {
	yaml := strings.Replace(feWorkflow(`[1,2,3]`, okBody, ", max_items: 2", ""), "outcomes: {success: ok, partial: part, failure: bad}",
		"outcomes: {success: ok, partial: part, failure: bad, exhausted: capped}", 1)
	yaml = strings.Replace(yaml, "  bad: {status: ok}", "  bad: {status: ok}\n  capped: {status: ok}", 1)
	_, instr, dir := feRun(t, yaml)
	wantTerminal(t, instr, "capped")
	rs := mustReplay(t, dir)
	if !strings.Contains(rs.LastError, "3") || !strings.Contains(rs.LastError, "2") {
		t.Errorf("LastError = %q, want it to name length 3 and cap 2", rs.LastError)
	}
}

func TestForeach_BodyRetryPerItem(t *testing.T) {
	// Item "b" hard-fails (exit 1) on its first try only.
	body := `    run: |-
      f=flaky-${item}
      if [ ${item} = b ] && [ ! -e "$f" ]; then touch "$f"; exit 1; fi
      printf '{"res":"%s"}' ${item}
    retry: {max_attempts: 3, backoff: 1ms}
    writes: [res]
    postcondition: {all_set: [res]}`
	_, instr, dir := feRun(t, feWorkflow(`["a","b"]`, body, "", ""))
	wantTerminal(t, instr, "ok")
	got := collectOf(t, dir)
	if got[1].(map[string]any)["outcome"] != "success" || got[1].(map[string]any)["error"] != nil {
		t.Errorf("entry 1 = %v, want recovered success with null error", got[1])
	}
}

func TestForeach_BodyRetryExhaustedIsFailure(t *testing.T) {
	body := `    run: "exit 3"
    retry: {max_attempts: 2, backoff: 1ms}`
	_, instr, _ := feRun(t, feWorkflow(`["a"]`, body, "", ""))
	wantTerminal(t, instr, "bad")
}

func TestForeach_RendersItemAndIndex(t *testing.T) {
	body := `    run: |-
      printf %s ${item} > item-${item_index}.txt`
	e, instr, _ := feRun(t, feWorkflow(`["a b", {"k":[1,2]}, 7, true]`, body, "", ""))
	wantTerminal(t, instr, "ok")
	for i, want := range []string{"a b", `{"k":[1,2]}`, "7", "true"} {
		data, err := os.ReadFile(filepath.Join(e.Root, "item-"+strconv.Itoa(i)+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Errorf("item %d rendered as %q, want %q (string raw, others compact JSON)", i, data, want)
		}
	}
}

func TestForeach_ItemExportedAsEnv(t *testing.T) {
	body := `    run: |-
      : ${item} ${item_index}
      printf '{"res":"%s-%s"}' "$PAWL_ITEM" "$PAWL_ITEM_INDEX"
    writes: [res]`
	_, instr, dir := feRun(t, feWorkflow(`["a"]`, body, "", ""))
	wantTerminal(t, instr, "ok")
	if res := collectOf(t, dir)[0].(map[string]any)["writes"].(map[string]any)["res"]; res != "a-0" {
		t.Errorf("env-exported item = %v, want a-0", res)
	}
}

func TestForeach_ItemInPostconditionAndFirstToken(t *testing.T) {
	// ${item} as a postcondition command argument, and as the first token
	// of a command (a would-be path): neither may be reparsed.
	body := `    run: |-
      printf '{"res":"%s"}' ${item}
    writes: [res]
    postcondition: {command: 'test ${item} = "$PAWL_ITEM"'}`
	_, instr, _ := feRun(t, feWorkflow(`["a b"]`, body, "", ""))
	wantTerminal(t, instr, "ok")
}

func TestForeach_ItemInjectionIsInert(t *testing.T) {
	evil := []string{`x'; touch PWNED; '`, `$(touch PWNED)`, "`touch PWNED`"}
	lit, _ := json.Marshal(evil)
	body := `    run: |-
      printf %s ${item}
    postcondition: {command: 'true ${item}'}`
	e, instr, _ := feRun(t, feWorkflow(string(lit), body, "", ""))
	wantTerminal(t, instr, "ok")
	if _, err := os.Stat(filepath.Join(e.Root, "PWNED")); err == nil {
		t.Fatal("PWNED was created: an item value reached a live shell context")
	}
}

func TestForeach_InvariantAtJoinSeesCollect(t *testing.T) {
	// The invariant fails unless collect is non-empty whenever fan has
	// transitioned; it records what it saw so we can prove the join's
	// invariant run observed collect (written before the invariant).
	extra := `invariants:
  - id: record
    check: 'echo "${out}" >> inv-seen; true'
    message: "n/a"`
	e, instr, _ := feRun(t, feWorkflow(`["a","b"]`, okBody, "", extra))
	wantTerminal(t, instr, "ok")
	data, err := os.ReadFile(filepath.Join(e.Root, "inv-seen"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "outcome:success") {
		t.Errorf("invariant never saw collect; saw %q", data)
	}
}

func TestForeach_InvariantViolatedBlocksAfterAllItems(t *testing.T) {
	extra := `invariants:
  - id: no-collect-failures
    check: ' ! echo "${out}" | grep -q failure'
    message: "an item failed"`
	e, instr, dir := feRun(t, feWorkflow(`["a","b","c"]`, failOnB, "", extra))
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" || term.Outcome != "invariant" {
		t.Fatalf("got %+v, want blocked by invariant", instr)
	}
	rs := mustReplay(t, dir)
	if len(rs.ItemOutcome["fan"]) != 3 || len(rs.PendingItems["fan"]) != 0 {
		t.Errorf("items not all transitioned: outcome=%v pending=%v", rs.ItemOutcome["fan"], rs.PendingItems["fan"])
	}
	if sawUngroupedTransition(t, dir, "fan") {
		t.Error("fan's own TRANSITION journaled despite the violated invariant")
	}
	_ = e
}

func TestForeach_InvariantEvaluatedOnceAtJoin(t *testing.T) {
	// A counter file appended to by the invariant: produce completes (1),
	// then fan's join (1) — never once per item. Three items, so a per-item
	// evaluation would make it 2+3.
	extra := `invariants:
  - id: count
    check: 'echo x >> inv-count; true'
    message: "n/a"`
	e, instr, _ := feRun(t, feWorkflow(`["a","b","c"]`, okBody, "", extra))
	wantTerminal(t, instr, "ok")
	data, err := os.ReadFile(filepath.Join(e.Root, "inv-count"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "x"); n != 2 {
		t.Errorf("invariant ran %d times, want 2 (produce + fan's join)", n)
	}
}

// truncateJournalBefore rewrites dir's events.jsonl keeping only the events
// before the first one for which stop returns true, simulating a crash at
// that point.
func truncateJournalBefore(t *testing.T, dir string, stop func(journal.Event) bool) {
	t.Helper()
	path := filepath.Join(dir, "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	events, err := journal.ReadEvents(dir)
	if err != nil {
		t.Fatal(err)
	}
	keep := -1
	for i, ev := range events {
		if stop(ev) {
			keep = i
			break
		}
	}
	if keep < 0 {
		t.Fatal("truncateJournalBefore: stop never matched")
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines[:keep], "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

const countingBody = `    run: |-
      echo ${item} >> ran.log
      printf '{"res":"%s"}' ${item}
    writes: [res]
    postcondition: {all_set: [res]}`

func ranLog(t *testing.T, root string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "ran.log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func TestForeach_ResumeRerunsOnlyPendingItems(t *testing.T) {
	e, instr, dir := feRun(t, feWorkflow(`["a","b","c"]`, countingBody, "", ""))
	wantTerminal(t, instr, "ok")
	// Crash after item 1 entered and ran, before its TRANSITION.
	truncateJournalBefore(t, dir, func(ev journal.Event) bool {
		return ev.Kind == journal.KindTransition && ev.Group == "fan" && ev.Item != nil && *ev.Item == 1
	})
	rs := mustReplay(t, dir)
	if !rs.PendingItems["fan"][1] || rs.ItemOutcome["fan"][0] != "success" {
		t.Fatalf("setup: pending=%v outcome=%v", rs.PendingItems["fan"], rs.ItemOutcome["fan"])
	}
	instr, err := e.Resume("run1", true)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	wantTerminal(t, instr, "ok")
	if got := ranLog(t, e.Root); !reflect.DeepEqual(got, []string{"a", "b", "c", "b", "c"}) {
		t.Errorf("ran.log = %v, want a b c then only b c re-run", got)
	}
	got := collectOf(t, dir)
	if len(got) != 3 || got[0].(map[string]any)["outcome"] != "success" {
		t.Errorf("collect = %v", got)
	}
}

func TestForeach_ResumeBeforeAnyItemEntered(t *testing.T) {
	e, instr, dir := feRun(t, feWorkflow(`["a","b"]`, countingBody, "", ""))
	wantTerminal(t, instr, "ok")
	truncateJournalBefore(t, dir, func(ev journal.Event) bool {
		return ev.Kind == journal.KindStepEnter && ev.Group == "fan"
	})
	instr, err := e.Resume("run1", true)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	wantTerminal(t, instr, "ok")
	if got := ranLog(t, e.Root); !reflect.DeepEqual(got, []string{"a", "b", "a", "b"}) {
		t.Errorf("ran.log = %v, want every item run after the crash", got)
	}
	if got := collectOf(t, dir); len(got) != 2 {
		t.Errorf("collect = %v", got)
	}
}

func TestForeach_ResumeAfterBlockedReentersFresh(t *testing.T) {
	extra := `invariants:
  - id: no-collect-failures
    check: ' ! echo "${out}" | grep -q failure'
    message: "an item failed"`
	e, instr, _ := feRun(t, feWorkflow(`["a","b"]`, failOnB, "", extra))
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want blocked", instr)
	}
	// b still fails, so re-entering the whole step blocks again; what
	// matters is that Resume does not error or loop.
	instr, err := e.Resume("run1", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if term, ok := instr.(Terminal); !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want blocked again", instr)
	}
}

func TestForeach_ReentryUsesNewListAndFreshState(t *testing.T) {
	// produce is re-entered from fan's partial route; its list shrinks on
	// the second visit (a marker file decides), so item "b" disappears and
	// the second fan-out must see only the new list with no stale state.
	yaml := `
workflow: fe-loop
start: produce
state:
  xs: {type: json}
  out: {type: json, default: []}
  res: {type: string, default: ""}
steps:
  - id: produce
    kind: deterministic
    run: |-
      if [ -e second ]; then echo '{"xs":["a"]}'; else touch second; echo '{"xs":["a","b"]}'; fi
    writes: [xs]
    max_visits: 3
    next: fan
  - id: fan
    kind: parallel
    foreach: {over: xs, body: one, collect: out}
    max_visits: 3
    outcomes: {success: ok, partial: produce, failure: bad}
  - id: one
    kind: deterministic
` + failOnB + `
terminal:
  ok: {status: ok}
  bad: {status: ok}
`
	_, instr, dir := feRun(t, yaml)
	wantTerminal(t, instr, "ok")
	rs := mustReplay(t, dir)
	if !reflect.DeepEqual(rs.ForeachItems["fan"], []any{"a"}) {
		t.Errorf("ForeachItems = %v, want the new list [a]", rs.ForeachItems["fan"])
	}
	got := collectOf(t, dir)
	if len(got) != 1 || got[0].(map[string]any)["outcome"] != "success" {
		t.Errorf("collect = %v, want only the fresh single success", got)
	}
}

func TestForeach_MaxVisitsCapsStep(t *testing.T) {
	yaml := `
workflow: fe-cap
start: produce
state:
  xs: {type: json}
  out: {type: json, default: []}
  res: {type: string, default: ""}
steps:
  - id: produce
    kind: deterministic
    run: |-
      echo '{"xs":["a","b"]}'
    writes: [xs]
    max_visits: 10
    next: fan
  - id: fan
    kind: parallel
    foreach: {over: xs, body: one, collect: out}
    max_visits: 2
    outcomes: {success: ok, partial: produce, failure: bad}
  - id: one
    kind: deterministic
` + failOnB + `
terminal:
  ok: {status: ok}
  bad: {status: ok}
`
	_, instr, _ := feRun(t, yaml)
	term, ok := instr.(Terminal)
	if !ok || term.Status != "blocked" {
		t.Fatalf("got %+v, want blocked once fan's max_visits binds (exhausted)", instr)
	}
}

func TestForeach_SubmitRefusedForBody(t *testing.T) {
	// Park the run on an agentic step after the join so it is live, then
	// submit naming the (deterministic, never-dispatchable) body.
	yaml := strings.Replace(feWorkflow(`["a"]`, okBody, "", ""), "success: ok,", "success: review,", 1)
	yaml = strings.Replace(yaml, "  - id: one\n", "  - id: review\n    kind: agentic\n    description: review\n    postcondition: \"true\"\n    writes: {rv: {type: string}}\n    next: ok\n  - id: one\n", 1)
	yaml = strings.Replace(yaml, "state:\n", "state:\n  rv: {type: string, default: \"\"}\n", 1)
	e, instr, _ := feRun(t, yaml)
	if _, ok := instr.(Dispatch); !ok {
		t.Fatalf("got %T %+v, want Dispatch at review", instr, instr)
	}
	_, err := e.Submit("run1", "one", 1, []byte(`{}`))
	if err == nil {
		t.Fatal("Submit for a foreach body: want an error")
	}
	if !strings.Contains(err.Error(), "foreach") {
		t.Errorf("error = %v, want one that names foreach", err)
	}
}

// TestForeach_ResumedItemStartsClean: the crashed try of item 0 wrote key a;
// the re-run writes only b, so collect's writes for that item hold only b
// (and its stale diagnostic is not reported either).
func TestForeach_ResumedItemStartsClean(t *testing.T) {
	body := `    run: |-
      if [ -e marker ]; then printf '{"b":"new"}'; else touch marker; printf '{"a":"old"}'; fi
    writes: [a, b]`
	yaml := strings.Replace(feWorkflow(`["x","y"]`, body, "", ""), "state:\n", "state:\n  a: {type: string, default: \"\"}\n  b: {type: string, default: \"\"}\n", 1)
	e, instr, dir := feRun(t, yaml)
	wantTerminal(t, instr, "ok")
	// Crash with item 0 entered and written, but not transitioned.
	truncateJournalBefore(t, dir, func(ev journal.Event) bool {
		return ev.Kind == journal.KindTransition && ev.Group == "fan" && ev.Item != nil && *ev.Item == 0
	})
	instr, err := e.Resume("run1", true)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	wantTerminal(t, instr, "ok")
	w := collectOf(t, dir)[0].(map[string]any)["writes"]
	if want := map[string]any{"b": "new"}; !reflect.DeepEqual(w, want) {
		t.Errorf("item 0 writes = %v, want %v (the crashed try's key a must be gone)", w, want)
	}
}

// visitsAfterCrashResume crashes a run just before the first event for which
// stop is true, resumes it, and returns step's Visits before and after.
func visitsAfterCrashResume(t *testing.T, yaml, step string, stop func(journal.Event) bool) (before, after int) {
	t.Helper()
	e, _, dir := feRun(t, yaml)
	truncateJournalBefore(t, dir, stop)
	before = mustReplay(t, dir).Visits[step]
	if _, err := e.Resume("run1", true); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	return before, mustReplay(t, dir).Visits[step]
}

// TestResume_VisitsMatchPlainAndForeach: crash-resuming a plain
// deterministic step re-journals a STEP_ENTER; a foreach step crashed before
// its Items STEP_ENTER must change Visits by exactly the same amount.
func TestResume_VisitsMatchPlainAndForeach(t *testing.T) {
	plain := `
workflow: plain
start: a
steps:
  - id: a
    kind: deterministic
    run: "true"
    next: done
terminal: {done: {status: ok}}
`
	pb, pa := visitsAfterCrashResume(t, plain, "a", func(ev journal.Event) bool {
		return ev.Kind == journal.KindTransition && ev.Step == "a"
	})
	// Empty list: the foreach STEP_ENTER carries no Items, so resume cannot
	// continue from a snapshot. Cut before the collect WRITES.
	fb, fa := visitsAfterCrashResume(t, feWorkflow(`[]`, okBody, "", ""), "fan", func(ev journal.Event) bool {
		return ev.Kind == journal.KindWrites && ev.Step == "fan"
	})
	t.Logf("plain: %d -> %d, foreach: %d -> %d", pb, pa, fb, fa)
	if pb != fb {
		t.Fatalf("setup: visits before resume differ: plain %d, foreach %d", pb, fb)
	}
	if pa-pb != fa-fb {
		t.Errorf("resume changed Visits by %d for a plain step but %d for a foreach step", pa-pb, fa-fb)
	}
}
