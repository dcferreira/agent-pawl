package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// newForeachAgenticProject lays docs/examples/foreach-agentic out the way
// pawl resolves a named workflow (YAML at .claude/workflows/<name>.yaml with
// scripts/ and inputs/ alongside it).
func newForeachAgenticProject(t *testing.T) *project {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	wf := filepath.Join(dir, ".claude", "workflows")
	src := filepath.Join(root, "docs", "examples", "foreach-agentic")
	copyFile(t, filepath.Join(src, "workflow.yaml"), mustMkdirFile(t, wf, "foreach-agentic.yaml"), 0o644)
	for _, sub := range []string{"scripts", "inputs"} {
		entries, err := os.ReadDir(filepath.Join(src, sub))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				t.Fatal(err)
			}
			copyFile(t, filepath.Join(src, sub, e.Name()), mustMkdirFile(t, filepath.Join(wf, sub), e.Name()), info.Mode().Perm())
		}
	}
	return &project{root: dir, stateDir: t.TempDir()}
}

func mustMkdirFile(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

// trueCounts are the word counts of the bundled inputs/*.txt, in name order.
var trueCounts = []int{3, 5, 4}

var (
	runIDRe   = regexp.MustCompile(`(?m)^DISPATCH_PARALLEL (\S+) count_all$`)
	itemHdrRe = regexp.MustCompile(`(?m)^  DISPATCH \S+ count_words\[(\d+)\]$`)
)

// agenticEntry is foreachEntry with the integer-typed write kept numeric.
type agenticEntry struct {
	Index   int            `json:"index"`
	Item    string         `json:"item"`
	Outcome string         `json:"outcome"`
	Writes  map[string]int `json:"writes"`
	Error   *string        `json:"error"`
}

func agenticCollect(t *testing.T, out string) []agenticEntry {
	t.Helper()
	start, end := strings.Index(out, "["), strings.LastIndex(out, "]")
	if start < 0 || end < start {
		t.Fatalf("no collected json array in output:\n%s", out)
	}
	var got []agenticEntry
	if err := json.Unmarshal([]byte(out[start:end+1]), &got); err != nil {
		t.Fatalf("collected results are not json: %v\n%s", err, out)
	}
	return got
}

func runID(t *testing.T, out string) string {
	t.Helper()
	m := runIDRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no DISPATCH_PARALLEL for count_all in:\n%s", out)
	}
	return m[1]
}

func (p *project) submitItem(t *testing.T, run string, item, count int) (string, int) {
	t.Helper()
	return p.run(t, "submit", "--run", run, "--step", "count_words", "--item", fmt.Sprint(item),
		"--json", fmt.Sprintf(`{"word_count":%d}`, count))
}

// firstDispatch runs the example and checks the single DISPATCH_PARALLEL
// carries one nested block per item with its own --item submit line.
func firstDispatch(t *testing.T, p *project) string {
	t.Helper()
	out, code := p.run(t, "run", "foreach-agentic")
	if code != 0 {
		t.Fatalf("pawl run: exit %d\n%s", code, out)
	}
	if n := strings.Count(out, "DISPATCH_PARALLEL "); n != 2 { // opening + END line
		t.Fatalf("want exactly one DISPATCH_PARALLEL block, got %d marker lines:\n%s", n, out)
	}
	hdrs := itemHdrRe.FindAllStringSubmatch(out, -1)
	if len(hdrs) != len(trueCounts) {
		t.Fatalf("want %d item blocks, got %d:\n%s", len(trueCounts), len(hdrs), out)
	}
	run := runID(t, out)
	for i := range trueCounts {
		if hdrs[i][1] != fmt.Sprint(i) {
			t.Errorf("item block %d headed [%s]", i, hdrs[i][1])
		}
		want := fmt.Sprintf("submit with: pawl submit --run %s --step count_words --item %d --json", run, i)
		if !strings.Contains(out, want) {
			t.Errorf("missing submit line %q in:\n%s", want, out)
		}
		if !strings.Contains(out, fmt.Sprintf("  END DISPATCH %s count_words[%d]", run, i)) {
			t.Errorf("missing END DISPATCH for item %d", i)
		}
	}
	return run
}

// TestForeachAgenticRetryThenSuccess: one item is first answered wrongly, is
// re-dispatched alone at attempt 2 with the failure text, then answered
// correctly; the join resolves success.
func TestForeachAgenticRetryThenSuccess(t *testing.T) {
	p := newForeachAgenticProject(t)
	run := firstDispatch(t, p)

	// Out of order: item 2, then the wrong answer for item 1.
	out, code := p.submitItem(t, run, 2, trueCounts[2])
	if code != 0 || !strings.Contains(out, "~ item count_words[2] recorded") || !strings.Contains(out, "waiting on: count_words[0], count_words[1]") {
		t.Fatalf("item 2 submit: exit %d\n%s", code, out)
	}
	out, code = p.submitItem(t, run, 1, 99)
	if code != 0 {
		t.Fatalf("wrong item 1 submit: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "DISPATCH_PARALLEL "+run+" count_all") ||
		!strings.Contains(out, "count_words[1]") || strings.Contains(out, "count_words[0]") ||
		!strings.Contains(out, "attempt: 2 of 2") || !strings.Contains(out, "previous attempt failed") {
		t.Fatalf("expected a retry block for item 1 alone at attempt 2 with the failure text:\n%s", out)
	}
	// Item 0 is still outstanding while item 1's retry was dispatched.
	out, code = p.submitItem(t, run, 1, trueCounts[1])
	if code != 0 || !strings.Contains(out, "waiting on: count_words[0]") {
		t.Fatalf("corrected item 1 submit: exit %d\n%s", code, out)
	}
	out, code = p.submitItem(t, run, 0, trueCounts[0])
	if code != 0 {
		t.Fatalf("item 0 submit: exit %d\n%s", code, out)
	}
	if m := terminalLine.FindStringSubmatch(out); m == nil || m[1] != "ok" || !strings.Contains(out, "All counts verified:") {
		t.Fatalf("expected success terminal, got:\n%s", out)
	}
	got := agenticCollect(t, out)
	if len(got) != len(trueCounts) {
		t.Fatalf("collect has %d entries:\n%s", len(got), out)
	}
	for i, g := range got {
		if g.Index != i || g.Outcome != "success" || g.Writes["word_count"] != trueCounts[i] {
			t.Errorf("entry %d = %+v, want success with word_count %d", i, g, trueCounts[i])
		}
	}
}

// TestForeachAgenticExhaustedPartial: one item fails both attempts, so the
// join resolves partial and its collect entry carries the exhaustion error.
func TestForeachAgenticExhaustedPartial(t *testing.T) {
	p := newForeachAgenticProject(t)
	run := firstDispatch(t, p)

	for _, i := range []int{0, 2} {
		if out, code := p.submitItem(t, run, i, trueCounts[i]); code != 0 {
			t.Fatalf("item %d submit: exit %d\n%s", i, code, out)
		}
	}
	out, _ := p.submitItem(t, run, 1, 99)
	if !strings.Contains(out, "attempt: 2 of 2") {
		t.Fatalf("expected retry block for item 1:\n%s", out)
	}
	out, code := p.submitItem(t, run, 1, 98)
	if code != 0 {
		t.Fatalf("final item 1 submit: exit %d\n%s", code, out)
	}
	if m := terminalLine.FindStringSubmatch(out); m == nil || m[1] != "ok" || !strings.Contains(out, "Some counts failed verification:") {
		t.Fatalf("expected partial terminal, got:\n%s", out)
	}
	got := agenticCollect(t, out)
	for i, g := range got {
		want := "success"
		if i == 1 {
			want = "failure"
		}
		if g.Outcome != want {
			t.Errorf("entry %d outcome = %s, want %s", i, g.Outcome, want)
		}
	}
	if got[1].Error == nil || !strings.Contains(*got[1].Error, "attempts") {
		t.Errorf("item 1 error should say it exhausted its attempts, got %v", got[1].Error)
	}
}
