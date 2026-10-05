package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// newForeachProject lays docs/examples/foreach-fanout out the way pawl
// resolves a named workflow: the YAML at .claude/workflows/foreach-fanout.yaml
// with scripts/ and inputs/ alongside it (as the example itself has them
// next to workflow.yaml).
func newForeachProject(t *testing.T) *project {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	wf := filepath.Join(dir, ".claude", "workflows")
	src := filepath.Join(root, "docs", "examples", "foreach-fanout")
	if err := os.MkdirAll(filepath.Join(wf, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wf, "inputs"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(src, "workflow.yaml"), filepath.Join(wf, "foreach-fanout.yaml"), 0o644)
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
			copyFile(t, filepath.Join(src, sub, e.Name()), filepath.Join(wf, sub, e.Name()), info.Mode().Perm())
		}
	}
	return &project{root: dir, stateDir: t.TempDir()}
}

type foreachEntry struct {
	Index   int               `json:"index"`
	Item    string            `json:"item"`
	Outcome string            `json:"outcome"`
	Writes  map[string]string `json:"writes"`
	Error   *string           `json:"error"`
}

// terminalCollect parses the collected results out of the TERMINAL message
// (everything after the first "[" up to the END TERMINAL line).
func terminalCollect(t *testing.T, out string) []foreachEntry {
	t.Helper()
	start := strings.Index(out, "[")
	end := strings.LastIndex(out, "]")
	if start < 0 || end < start {
		t.Fatalf("no collected json array in output:\n%s", out)
	}
	var got []foreachEntry
	if err := json.Unmarshal([]byte(out[start:end+1]), &got); err != nil {
		t.Fatalf("collected results are not json: %v\n%s", err, out)
	}
	return got
}

var terminalLine = regexp.MustCompile(`(?m)^TERMINAL\s+\S+\s+(\S+)`)

// TestForeachFanoutPartial runs the example as shipped: the runtime-discovered
// list has four items, one of which contains the forbidden marker, so the
// join resolves partial and the run ends on the some_dirty terminal.
func TestForeachFanoutPartial(t *testing.T) {
	p := newForeachProject(t)
	out, code := p.run(t, "run", "foreach-fanout")
	if code != 0 {
		t.Fatalf("pawl run foreach-fanout: exit %d, output:\n%s", code, out)
	}
	if m := terminalLine.FindStringSubmatch(out); m == nil || m[1] != "ok" {
		t.Fatalf("expected a TERMINAL with status ok, got:\n%s", out)
	}
	if !strings.Contains(out, "Some items failed:") {
		t.Fatalf("expected the some_dirty (partial) terminal message, got:\n%s", out)
	}
	got := terminalCollect(t, out)
	want := []struct{ item, outcome string }{
		{"alpha.txt", "success"}, {"bravo.txt", "success"},
		{"charlie.txt", "failure"}, {"delta.txt", "success"},
	}
	if len(got) != len(want) {
		t.Fatalf("collect has %d entries, want %d:\n%s", len(got), len(want), out)
	}
	for i, w := range want {
		g := got[i]
		if g.Index != i || g.Item != w.item || g.Outcome != w.outcome {
			t.Errorf("entry %d = %+v, want item %s outcome %s", i, g, w.item, w.outcome)
		}
		if w.outcome == "success" && g.Writes["verdict"] != "clean" {
			t.Errorf("entry %d: want per-item write verdict=clean, got %v", i, g.Writes)
		}
		if w.outcome == "failure" && (g.Error == nil || !strings.Contains(*g.Error, "forbidden marker")) {
			t.Errorf("entry %d: want the failing script's diagnostic as error, got %v", i, g.Error)
		}
	}
}

// TestForeachFanoutAllPass overrides the forbidden marker so no item matches:
// every item succeeds and the join resolves success.
func TestForeachFanoutAllPass(t *testing.T) {
	p := newForeachProject(t)
	out, code := p.run(t, "run", "foreach-fanout", "forbidden=NEVER-PRESENT")
	if code != 0 {
		t.Fatalf("pawl run: exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "Every item is clean:") {
		t.Fatalf("expected the all_clean (success) terminal message, got:\n%s", out)
	}
	for i, g := range terminalCollect(t, out) {
		if g.Outcome != "success" {
			t.Errorf("entry %d outcome = %s, want success", i, g.Outcome)
		}
	}
}
