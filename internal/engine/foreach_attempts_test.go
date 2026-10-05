package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// agAttBody is an agentic foreach body with a per-item budget of 3.
const agAttBody = `    description: "process ${item} (#${item_index})"
    attempts: 3
    writes: {res: {type: string}}
    postcondition: {all_set: [res]}`

func agAtt(listJSON, bodyYAML string) string {
	return feAgentic(listJSON, bodyYAML, "", "")
}

// wantRetry asserts instr is a DispatchParallel carrying exactly one item's
// retry Dispatch (item, attempt) with failure text, and returns it.
func wantRetry(t *testing.T, instr Instruction, item, attempt int) Dispatch {
	t.Helper()
	dp, ok := instr.(DispatchParallel)
	if !ok || dp.Step != "fan" || len(dp.Agentic) != 1 {
		t.Fatalf("got %T %+v, want a one-item DispatchParallel for fan", instr, instr)
	}
	d := dp.Agentic[0]
	if d.Step != "one" || itemIdx(d) != item || d.Attempt != attempt {
		t.Fatalf("retry dispatch = step %q item %d attempt %d, want one/%d/%d", d.Step, itemIdx(d), d.Attempt, item, attempt)
	}
	if d.PreviousFailure == "" {
		t.Errorf("retry dispatch carries no previous failure text")
	}
	return d
}

func TestForeachAttempts_RetryDispatchesOnlyThatItemWhileSiblingsPending(t *testing.T) {
	e, _, dir := agStart(t, agAtt(`["a","b","c"]`, agAttBody))
	instr, err := e.SubmitItem("run1", "one", 1, 1, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	d := wantRetry(t, instr, 1, 2)
	if d.Description != "process b (#1)" {
		t.Errorf("Description = %q", d.Description)
	}
	rs := mustReplay(t, dir)
	if !rs.PendingItems["fan"][0] || !rs.PendingItems["fan"][1] || !rs.PendingItems["fan"][2] || len(rs.ItemOutcome["fan"]) != 0 {
		t.Errorf("pending=%v outcome=%v, want all three still pending", rs.PendingItems["fan"], rs.ItemOutcome["fan"])
	}
	if rs.ItemAttempt["fan"][1] != 2 || rs.ItemAttempt["fan"][0] != 1 {
		t.Errorf("ItemAttempt = %v", rs.ItemAttempt["fan"])
	}
	// item retries are not visits of anything
	if rs.Visits["one"] != 0 || rs.Visits["fan"] != 1 || rs.Visits["produce"] != 1 {
		t.Errorf("Visits = %v, want unchanged by the retry", rs.Visits)
	}
	if rs.LastError != "" {
		t.Errorf("LastError = %q, item failures stay item-scoped", rs.LastError)
	}
}

func TestForeachAttempts_RetryPassesThenSuccess(t *testing.T) {
	e, _, dir := agStart(t, agAtt(`["a","b"]`, agAttBody))
	if _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`)); err != nil {
		t.Fatal(err)
	}
	instr, err := e.SubmitItem("run1", "one", 1, 1, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantRetry(t, instr, 1, 2)
	instr, err = e.SubmitItem("run1", "one", 1, 2, []byte(`{"res":"B"}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "ok")
	got := collectOf(t, dir)
	if got[1].(map[string]any)["outcome"] != "success" || got[1].(map[string]any)["error"] != nil {
		t.Errorf("item 1 = %v, want success with no error", got[1])
	}
}

func TestForeachAttempts_ExhaustedAfterBudget(t *testing.T) {
	e, _, dir := agStart(t, agAtt(`["a"]`, agAttBody))
	instr, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantRetry(t, instr, 0, 2)
	instr, err = e.SubmitItem("run1", "one", 0, 2, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantRetry(t, instr, 0, 3)
	instr, err = e.SubmitItem("run1", "one", 0, 3, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "bad")
	m := collectOf(t, dir)[0].(map[string]any)
	text, _ := m["error"].(string)
	if m["outcome"] != "failure" || !strings.Contains(text, "exhausted") || !strings.Contains(text, "3 attempts") {
		t.Errorf("item = %v, want failure whose error says it exhausted its 3 attempts", m)
	}
	// the last failure text is included
	last := strings.TrimSpace(strings.SplitN(text, "last failure:", 2)[len(strings.SplitN(text, "last failure:", 2))-1])
	if !strings.Contains(text, "last failure:") || last == "" {
		t.Errorf("error %q does not include the last failure text", text)
	}
}

func TestForeachAttempts_StaleSubmitRefused(t *testing.T) {
	e, _, dir := agStart(t, agAtt(`["a","b"]`, agAttBody))
	if _, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	_, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`))
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "attempt 2") {
		t.Fatalf("err = %v, want ErrRefused naming the current attempt 2", err)
	}
	if rs := mustReplay(t, dir); !rs.PendingItems["fan"][0] || len(rs.ItemOutcome["fan"]) != 0 {
		t.Errorf("a refused stale submit changed state: %v %v", rs.PendingItems["fan"], rs.ItemOutcome["fan"])
	}
	// a future attempt is refused too
	if _, err := e.SubmitItem("run1", "one", 0, 3, []byte(`{"res":"A"}`)); !errors.Is(err, ErrRefused) {
		t.Errorf("err = %v, want ErrRefused for attempt 3", err)
	}
	if _, err := e.SubmitItem("run1", "one", 0, 2, []byte(`{"res":"A"}`)); err != nil {
		t.Errorf("current attempt refused: %v", err)
	}
}

func TestForeachAttempts_SiblingSubmitsInterleavedWithRetry(t *testing.T) {
	e, _, dir := agStart(t, agAtt(`["a","b","c"]`, agAttBody))
	instr, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{"res":"A"}`))
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := instr.(ItemRecorded); !ok || !reflect.DeepEqual(rec.Remaining, []int{1, 2}) {
		t.Fatalf("got %T %+v", instr, instr)
	}
	instr, err = e.SubmitItem("run1", "one", 1, 1, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantRetry(t, instr, 1, 2)
	instr, err = e.SubmitItem("run1", "one", 2, 1, []byte(`{"res":"C"}`))
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := instr.(ItemRecorded); !ok || !reflect.DeepEqual(rec.Remaining, []int{1}) {
		t.Fatalf("got %T %+v, want item 1 still outstanding", instr, instr)
	}
	instr, err = e.SubmitItem("run1", "one", 1, 2, []byte(`{"res":"B"}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "ok")
	for i, c := range collectOf(t, dir) {
		if c.(map[string]any)["outcome"] != "success" {
			t.Errorf("item %d = %v", i, c)
		}
	}
}

func TestForeachAttempts_AutomaticKeyFollowsFailureText(t *testing.T) {
	body := `    description: "p ${item}"
    attempts: 5
    writes: {res: {type: string}}
    postcondition: {all_set: [res]}`
	e, _, dir := agStart(t, agAtt(`["a"]`, body))
	keyAfter := func(attempt int, result string) string {
		t.Helper()
		if _, err := e.SubmitItem("run1", "one", 0, attempt, []byte(result)); err != nil {
			t.Fatal(err)
		}
		return mustReplay(t, dir).ItemAttemptKey["fan"][0]
	}
	k1 := keyAfter(1, `{}`)       // postcondition failure text A
	k2 := keyAfter(2, `{}`)       // the same text again
	k3 := keyAfter(3, `not json`) // a different failure text
	if k1 == "" || k1 != k2 || k3 == "" || k3 == k1 {
		t.Errorf("keys = %q %q %q, want a stable hash per failure text, different across texts", k1, k2, k3)
	}
}

func TestForeachAttempts_AttemptKeyOverride(t *testing.T) {
	body := `    description: "p ${item}"
    attempts: 3
    attempt_key: "k-${item}-${item_index}"
    writes: {res: {type: string}}
    postcondition: {all_set: [res]}`
	e, _, dir := agStart(t, agAtt(`["a","b"]`, body))
	rs := mustReplay(t, dir)
	if rs.ItemAttemptKey["fan"][0] != "k-a-0" || rs.ItemAttemptKey["fan"][1] != "k-b-1" {
		t.Fatalf("keys = %v, want the rendered template per item", rs.ItemAttemptKey["fan"])
	}
	if _, err := e.SubmitItem("run1", "one", 1, 1, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SubmitItem("run1", "one", 1, 2, []byte(`oops`)); err != nil {
		t.Fatal(err)
	}
	rs = mustReplay(t, dir)
	if rs.ItemAttemptKey["fan"][1] != "k-b-1" || rs.ItemAttempt["fan"][1] != 3 {
		t.Errorf("key=%q attempt=%d, want the override key to persist across differing failures", rs.ItemAttemptKey["fan"][1], rs.ItemAttempt["fan"][1])
	}
}

func TestForeachAttempts_RunawayCapAtForeachMaxVisits(t *testing.T) {
	body := strings.Replace(agAttBody, "attempts: 3", "attempts: 8", 1)
	yaml := strings.Replace(agAtt(`["a"]`, body), "    outcomes: {success: ok,", "    max_visits: 3\n    outcomes: {success: ok,", 1)
	e, _, dir := agStart(t, yaml)
	for attempt := 1; attempt <= 2; attempt++ {
		instr, err := e.SubmitItem("run1", "one", 0, attempt, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		wantRetry(t, instr, 0, attempt+1)
	}
	instr, err := e.SubmitItem("run1", "one", 0, 3, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "bad")
	text, _ := collectOf(t, dir)[0].(map[string]any)["error"].(string)
	if !strings.Contains(text, "exhausted") || !strings.Contains(text, "max_visits") {
		t.Errorf("error = %q, want an exhausted error naming the max_visits cap", text)
	}
}

func TestForeachAttempts_ResumeMidRetryRedispatchesSameAttempt(t *testing.T) {
	e, _, dir := agStart(t, agAtt(`["a","b","c"]`, agAttBody))
	if _, err := e.SubmitItem("run1", "one", 2, 1, []byte(`{"res":"C"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SubmitItem("run1", "one", 1, 1, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	for crash := 0; crash < 2; crash++ { // a crash never advances a counter, however often it repeats
		instr, err := e.Resume("run1", true)
		if err != nil {
			t.Fatal(err)
		}
		dp, ok := instr.(DispatchParallel)
		if !ok || !dp.Interrupted || len(dp.Agentic) != 2 {
			t.Fatalf("crash %d: got %T %+v, want items 0 and 1", crash, instr, instr)
		}
		d0, d1 := dp.Agentic[0], dp.Agentic[1]
		if itemIdx(d0) != 0 || d0.Attempt != 1 || d0.PreviousFailure != "" || !d0.Interrupted {
			t.Errorf("item 0 = %+v", d0)
		}
		if itemIdx(d1) != 1 || d1.Attempt != 2 || d1.PreviousFailure == "" || !d1.Interrupted {
			t.Errorf("item 1 = %+v, want attempt 2 with the failure text, Interrupted", d1)
		}
		if rs := mustReplay(t, dir); rs.ItemAttempt["fan"][1] != 2 {
			t.Errorf("ItemAttempt after resume = %v", rs.ItemAttempt["fan"])
		}
	}
	// the budget survives resume: attempt 2 then 3 fail -> exhausted
	if _, err := e.SubmitItem("run1", "one", 1, 2, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SubmitItem("run1", "one", 1, 3, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if rs := mustReplay(t, dir); rs.ItemOutcome["fan"][1] != "failure" {
		t.Errorf("outcome = %v, want item 1 exhausted", rs.ItemOutcome["fan"])
	}
}

func TestForeachAttempts_NoAttemptsDeclaredStillFailsImmediately(t *testing.T) {
	e, _, dir := agStart(t, feAgentic(`["a"]`, agBody, "", ""))
	instr, err := e.SubmitItem("run1", "one", 0, 1, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal(t, instr, "bad")
	if text, _ := collectOf(t, dir)[0].(map[string]any)["error"].(string); strings.Contains(text, "exhausted") || text == "" {
		t.Errorf("error = %q, want the plain failure text", text)
	}
}
