package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// agBody is an agentic foreach body that returns {res} and requires it set.
const agBody = `    description: "process ${item} (#${item_index})"
    writes: {res: {type: string}}
    postcondition: {all_set: [res]}`

// feAgentic is feWorkflow with the body step made agentic.
func feAgentic(listJSON, bodyYAML, fanExtra, extra string) string {
	return strings.Replace(feWorkflow(listJSON, bodyYAML, fanExtra, extra),
		"  - id: one\n    kind: deterministic\n", "  - id: one\n    kind: agentic\n", 1)
}

func agStart(t *testing.T, yaml string) (*Engine, DispatchParallel, string) {
	t.Helper()
	e, instr, dir := feRun(t, yaml)
	dp, ok := instr.(DispatchParallel)
	if !ok {
		t.Fatalf("got %T %+v, want DispatchParallel", instr, instr)
	}
	return e, dp, dir
}

func itemIdx(d Dispatch) int {
	if d.Item == nil {
		return -1
	}
	return *d.Item
}

func TestForeachAgentic_DispatchesAllItemsInOneDispatchParallel(t *testing.T) {
	_, dp, dir := agStart(t, feAgentic(`["a","b","c"]`, agBody, "", ""))
	if dp.Step != "fan" || dp.Interrupted || len(dp.Agentic) != 3 {
		t.Fatalf("dp = %+v", dp)
	}
	for i, want := range []string{"a", "b", "c"} {
		d := dp.Agentic[i]
		if d.Step != "one" || itemIdx(d) != i {
			t.Errorf("Agentic[%d] step/item = %q/%d", i, d.Step, itemIdx(d))
		}
		wantDesc := "process " + want + " (#" + string(rune('0'+i)) + ")"
		if d.Description != wantDesc {
			t.Errorf("Agentic[%d].Description = %q, want %q", i, d.Description, wantDesc)
		}
	}
	rs := mustReplay(t, dir)
	if len(rs.PendingItems["fan"]) != 3 || rs.Cursor.Step != "fan" {
		t.Errorf("pending=%v cursor=%+v", rs.PendingItems["fan"], rs.Cursor)
	}
}

func TestForeachAgentic_OutOfOrderSubmitsThenJoin(t *testing.T) {
	e, _, dir := agStart(t, feAgentic(`["a","b","c"]`, agBody, "", ""))
	instr, err := e.SubmitItem("run1", "one", 2, 1, []byte(`{"res":"C"}`))
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := instr.(ItemRecorded)
	if !ok || rec.ForeachStep != "fan" || rec.BodyStep != "one" || rec.Item != 2 || !reflect.DeepEqual(rec.Remaining, []int{0, 1}) {
		t.Fatalf("got %T %+v", instr, instr)
	}
	instr, err = e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`))
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := instr.(ItemRecorded); !ok || !reflect.DeepEqual(rec.Remaining, []int{1}) {
		t.Fatalf("got %T %+v", instr, instr)
	}
	instr, err = e.SubmitItem("run1", "one", 1, 1, []byte(`{"res":"B"}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "ok")
	want := []any{
		entry(0, "a", "success", map[string]any{"res": "A"}, nil),
		entry(1, "b", "success", map[string]any{"res": "B"}, nil),
		entry(2, "c", "success", map[string]any{"res": "C"}, nil),
	}
	if got := collectOf(t, dir); !reflect.DeepEqual(got, want) {
		t.Errorf("collect = %#v\nwant %#v", got, want)
	}
	if _, leaked := mustReplay(t, dir).State["res"]; leaked {
		t.Error("item write leaked into global state")
	}
}

func TestForeachAgentic_PartialOnPostconditionFailure(t *testing.T) {
	e, _, dir := agStart(t, feAgentic(`["a","b"]`, agBody, "", ""))
	if _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`)); err != nil {
		t.Fatal(err)
	}
	instr, err := e.SubmitItem("run1", "one", 1, 1, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "part")
	got := collectOf(t, dir)
	if got[1].(map[string]any)["outcome"] != "failure" || got[1].(map[string]any)["error"] == nil {
		t.Errorf("item 1 = %v, want failure with an error", got[1])
	}
	if got[0].(map[string]any)["outcome"] != "success" {
		t.Errorf("item 0 = %v", got[0])
	}
	// the item's failure must not become the run's last_error
	if rs := mustReplay(t, dir); rs.LastError != "" {
		t.Errorf("LastError = %q, want item failure kept item-scoped", rs.LastError)
	}
}

func TestForeachAgentic_AllFailIsFailure(t *testing.T) {
	e, _, dir := agStart(t, feAgentic(`["a","b"]`, agBody, "", ""))
	if _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":1,"x":`)); err != nil { // not JSON
		t.Fatal(err)
	}
	instr, err := e.SubmitItem("run1", "one", 1, 1, []byte(`{"res":{"nested":true}}`)) // wrong type
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "bad")
	for i, c := range collectOf(t, dir) {
		m := c.(map[string]any)
		if m["outcome"] != "failure" || m["error"] == nil {
			t.Errorf("item %d = %v", i, m)
		}
		if len(m["writes"].(map[string]any)) != 0 {
			t.Errorf("item %d kept writes %v", i, m["writes"])
		}
	}
}

func TestForeachAgentic_SubmitRefusals(t *testing.T) {
	e, _, _ := agStart(t, feAgentic(`["a","b"]`, agBody, "", ""))
	if _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		do   func() error
		want string
	}{
		{"duplicate", func() error { _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`)); return err }, "item 0"},
		{"out of range", func() error { _, err := e.SubmitItem("run1", "one", 5, 1, []byte(`{"res":"A"}`)); return err }, "item 5"},
		{"negative", func() error { _, err := e.SubmitItem("run1", "one", -1, 1, []byte(`{"res":"A"}`)); return err }, "item -1"},
		{"missing item", func() error { _, err := e.Submit("run1", "one", 1, []byte(`{"res":"A"}`)); return err }, "--item"},
		{"item on non-foreach step", func() error { _, err := e.SubmitItem("run1", "fan", 0, 1, []byte(`{}`)); return err }, "not the body of a foreach"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.do()
			if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want ErrRefused containing %q", err, c.want)
			}
		})
	}
}

func TestForeachAgentic_ContextFailureFailsOnlyThatItem(t *testing.T) {
	ctxDir := t.TempDir() // holds a.txt and c.txt, but no b.txt
	for _, n := range []string{"a", "c"} {
		if err := os.WriteFile(filepath.Join(ctxDir, n+".txt"), []byte("ctx-"+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := `    description: "process ${item}"
    context: ["` + ctxDir + `/${item}.txt"]
    writes: {res: {type: string}}
    postcondition: {all_set: [res]}`
	e, dp, dir := agStart(t, feAgentic(`["a","b","c"]`, body, "", ""))
	if len(dp.Agentic) != 2 || itemIdx(dp.Agentic[0]) != 0 || itemIdx(dp.Agentic[1]) != 2 {
		t.Fatalf("Agentic = %+v, want items 0 and 2 only", dp.Agentic)
	}
	if got := dp.Agentic[1].Context[0].Value; got != "ctx-c" {
		t.Errorf("context value = %q", got)
	}
	rs := mustReplay(t, dir)
	if rs.ItemOutcome["fan"][1] != "failure" || rs.PendingItems["fan"][1] {
		t.Errorf("item 1 not failed: outcome=%v pending=%v", rs.ItemOutcome["fan"], rs.PendingItems["fan"])
	}
	if _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`)); err != nil {
		t.Fatal(err)
	}
	instr, err := e.SubmitItem("run1", "one", 2, 1, []byte(`{"res":"C"}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "part")
	if c := collectOf(t, dir)[1].(map[string]any); c["error"] == nil {
		t.Errorf("item 1 has no error: %v", c)
	}
}

func TestForeachAgentic_AllContextFailuresJoinImmediately(t *testing.T) {
	body := `    description: "x"
    context: ["` + t.TempDir() + `/${item}.txt"]
    writes: {res: {type: string}}
    postcondition: {all_set: [res]}`
	_, instr, _ := feRun(t, feAgentic(`["a","b"]`, body, "", ""))
	wantTerminal(t, instr, "bad")
}

func TestForeachAgentic_InjectionInContextIsInert(t *testing.T) {
	evil := []string{`x'; touch PWNED; '`, `$(touch PWNED)`, "`touch PWNED`"}
	lit, _ := json.Marshal(evil)
	body := `    description: "p ${item}"
    context: [!cmd "echo ${item}"]
    writes: {res: {type: string}}
    postcondition: {command: 'true ${item}'}`
	e, dp, _ := agStart(t, feAgentic(string(lit), body, "", ""))
	for i, d := range dp.Agentic {
		if strings.TrimSpace(d.Context[0].Value) != evil[i] {
			t.Errorf("item %d context = %q, want the literal %q", i, d.Context[0].Value, evil[i])
		}
		if _, err := e.SubmitItem("run1", "one", i, 1, []byte(`{"res":"ok"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Root, "PWNED")); err == nil {
		t.Fatal("PWNED created: an item value reached a live shell")
	}
}

func TestForeachAgentic_ItemInPostcondition(t *testing.T) {
	body := `    description: "p"
    writes: {res: {type: string}}
    postcondition: {command: 'test ${res} = ${item}'}`
	e, _, dir := agStart(t, feAgentic(`["a","b"]`, body, "", ""))
	if _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"a"}`)); err != nil {
		t.Fatal(err)
	}
	instr, err := e.SubmitItem("run1", "one", 1, 1, []byte(`{"res":"zzz"}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "part")
	if got := collectOf(t, dir); got[0].(map[string]any)["outcome"] != "success" || got[1].(map[string]any)["outcome"] != "failure" {
		t.Errorf("collect = %v", got)
	}
}

func TestForeachAgentic_InvariantOnceAtJoin(t *testing.T) {
	extra := `invariants:
  - id: count
    check: 'echo x >> inv-count; true'
    message: "n/a"`
	e, _, _ := agStart(t, feAgentic(`["a","b","c"]`, agBody, "", extra))
	for i := 0; i < 3; i++ {
		if _, err := e.SubmitItem("run1", "one", i, 1, []byte(`{"res":"r"}`)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(e.Root, "inv-count"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "x"); n != 2 {
		t.Errorf("invariant ran %d times, want 2 (produce + fan's join)", n)
	}
}

func TestForeachAgentic_ResumeRedispatchesOnlyOutstanding(t *testing.T) {
	e, _, dir := agStart(t, feAgentic(`["a","b","c"]`, agBody, "", ""))
	if _, err := e.SubmitItem("run1", "one", 1, 1, []byte(`{"res":"B"}`)); err != nil {
		t.Fatal(err)
	}
	instr, err := e.Resume("run1", true)
	if err != nil {
		t.Fatal(err)
	}
	dp, ok := instr.(DispatchParallel)
	if !ok || !dp.Interrupted || dp.Step != "fan" {
		t.Fatalf("got %T %+v", instr, instr)
	}
	if len(dp.Agentic) != 2 || itemIdx(dp.Agentic[0]) != 0 || itemIdx(dp.Agentic[1]) != 2 || !dp.Agentic[0].Interrupted {
		t.Fatalf("Agentic = %+v, want only items 0 and 2", dp.Agentic)
	}
	for _, i := range []int{0, 2} {
		instr, err = e.SubmitItem("run1", "one", i, 1, []byte(`{"res":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
	}
	wantTerminal(t, instr, "ok")
	if c := collectOf(t, dir)[1].(map[string]any); c["writes"].(map[string]any)["res"] != "B" {
		t.Errorf("item 1 was redone: %v", c)
	}
}

func TestForeachAgentic_ResumeNeverEnteredItems(t *testing.T) {
	e, _, dir := agStart(t, feAgentic(`["a","b"]`, agBody, "", ""))
	truncateJournalBefore(t, dir, func(ev journal.Event) bool {
		return ev.Kind == journal.KindStepEnter && ev.Group == "fan" && ev.Item != nil && *ev.Item == 1
	})
	instr, err := e.Resume("run1", true)
	if err != nil {
		t.Fatal(err)
	}
	dp, ok := instr.(DispatchParallel)
	if !ok || len(dp.Agentic) != 2 || !dp.Interrupted {
		t.Fatalf("got %T %+v", instr, instr)
	}
	if rs := mustReplay(t, dir); len(rs.PendingItems["fan"]) != 2 {
		t.Errorf("pending = %v", rs.PendingItems["fan"])
	}
}

func TestForeachAgentic_SubmitAfterJoinRefused(t *testing.T) {
	e, _, _ := agStart(t, feAgentic(`["a"]`, agBody, "", ""))
	instr, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "ok")
	if _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`)); !errors.Is(err, ErrAlreadyTerminal) {
		t.Errorf("err = %v, want ErrAlreadyTerminal", err)
	}
}
