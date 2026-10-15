package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// objectKeys returns the top-level keys of the JSON object in s, in the
// order they appear on the wire.
func objectKeys(t *testing.T, s string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not a JSON object: %q (%v)", s, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func startStatusRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	stdout, stderr, code := runCLI(t, append([]string{"pawl", "run", name}, args...))
	if code != 0 {
		t.Fatalf("pawl run %s: exit %d, stderr = %q", name, code, stderr)
	}
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "DISPATCH ") {
			return extractRunID(t, l)
		}
	}
	t.Fatalf("no DISPATCH in %q", stdout)
	return ""
}

func statusRuns(t *testing.T, out string) []map[string]any {
	t.Helper()
	raw := assertJSONObject(t, "status", out)
	var runs []map[string]any
	for _, r := range assertJSONArrayKey(t, "status", raw, "runs") {
		runs = append(runs, r.(map[string]any))
	}
	return runs
}

func TestStatusJSON_SchemaAndKeyOrder(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	startStatusRun(t, "sample", "name=Ada")

	out, _, code := runCLI(t, []string{"pawl", "status", "--json"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, want := objectKeys(t, out), []string{"schema_version", "root", "runs"}; !reflect.DeepEqual(got, want) {
		t.Errorf("top-level keys = %v, want %v", got, want)
	}
	raw := assertJSONObject(t, "status", out)
	if raw["schema_version"] != float64(1) {
		t.Errorf("schema_version = %#v, want 1", raw["schema_version"])
	}

	// Per-run key order.
	var top struct {
		Runs []json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &top); err != nil || len(top.Runs) != 1 {
		t.Fatalf("runs: %v %d", err, len(top.Runs))
	}
	wantKeys := []string{"run_id", "workflow", "workflow_path", "warning", "status", "step", "attempt", "visits", "state_keys", "reason", "soft"}
	if got := objectKeys(t, string(top.Runs[0])); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("run keys = %v, want %v", got, wantKeys)
	}

	// The empty case also carries schema_version first.
	if got := objectKeys(t, mustEmptyStatus(t)); !reflect.DeepEqual(got, []string{"schema_version", "root", "runs"}) {
		t.Errorf("empty keys = %v", got)
	}
}

func mustEmptyStatus(t *testing.T) string {
	t.Helper()
	return formatStatusJSONEmpty("/x")
}

func TestStatusJSON_WorkflowName(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeWorkflow(t, root, "manage-mr/foo", strings.Replace(sampleWorkflow, "workflow: sample\n", "workflow: foo\n", 1))
	writeContextFile(t, root, "notes.txt", "x\n")
	if err := os.WriteFile(filepath.Join(root, ".claude", "workflows", "manage-mr", "notes.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	top := startStatusRun(t, "sample", "name=Ada")
	nested := startStatusRun(t, "manage-mr/foo", "name=Ada")

	for _, tc := range []struct{ id, name, file string }{
		{top, "sample", "sample.yaml"},
		{nested, "manage-mr/foo", "manage-mr/foo.yaml"},
	} {
		out, errOut, code := runCLI(t, []string{"pawl", "status", "--run", tc.id, "--json"})
		if code != 0 {
			t.Fatalf("status %s: exit %d %q", tc.id, code, errOut)
		}
		r := statusRuns(t, out)[0]
		if r["workflow"] != tc.name {
			t.Errorf("workflow = %#v, want %q", r["workflow"], tc.name)
		}
		wantPath, _ := filepath.EvalSymlinks(filepath.Join(root, ".claude", "workflows", tc.file))
		gotPath, _ := r["workflow_path"].(string)
		if gotPath == "" || !strings.HasSuffix(gotPath, filepath.FromSlash(".claude/workflows/"+tc.file)) {
			t.Errorf("workflow_path = %q, want path ending in %s (real %s)", gotPath, tc.file, wantPath)
		}
	}
}

func TestStatusJSON_NonRoundTrippingWorkflowIsEmpty(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	id := startStatusRun(t, "sample", "name=Ada")
	// The file vanishes after the run started: no name resolves to it.
	if err := os.Remove(filepath.Join(root, ".claude", "workflows", "sample.yaml")); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCLI(t, []string{"pawl", "status", "--run", id, "--json"})
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	r := statusRuns(t, out)[0]
	if v, ok := r["workflow"]; !ok || v != "" {
		t.Errorf("workflow = %#v (present=%v), want \"\"", v, ok)
	}
	if p, _ := r["workflow_path"].(string); !strings.HasSuffix(p, "sample.yaml") {
		t.Errorf("workflow_path = %q, want the pinned absolute path", p)
	}
}

func TestStatusJSON_MultiRunReturnsAll(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeWorkflow(t, root, "sample2", strings.Replace(sampleWorkflow, "workflow: sample\n", "workflow: sample2\n", 1))
	writeContextFile(t, root, "notes.txt", "x\n")
	a := startStatusRun(t, "sample", "name=Ada")
	b := startStatusRun(t, "sample2", "name=Bea")

	out, errOut, code := runCLI(t, []string{"pawl", "status", "--json"})
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	runs := statusRuns(t, out)
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	ids := []string{runs[0]["run_id"].(string), runs[1]["run_id"].(string)}
	want := []string{a, b}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("run order = %v, want sorted by id %v", ids, want)
	}
	names := map[string]bool{}
	for _, r := range runs {
		names[r["workflow"].(string)] = true
	}
	if !names["sample"] || !names["sample2"] {
		t.Errorf("workflow names = %v", names)
	}
}

func TestStatusText_MultiRunStillRefuses(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeWorkflow(t, root, "sample2", strings.Replace(sampleWorkflow, "workflow: sample\n", "workflow: sample2\n", 1))
	writeContextFile(t, root, "notes.txt", "x\n")
	startStatusRun(t, "sample", "name=Ada")
	startStatusRun(t, "sample2", "name=Bea")
	stdout, stderr, code := runCLI(t, []string{"pawl", "status"})
	if code != 1 || stdout != "" || !strings.Contains(stderr, "--run") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// startBrokenPair starts a healthy and a second run, then returns both ids
// and the second one's run directory for the caller to break.
func startBrokenPair(t *testing.T) (root, good, bad, dir string) {
	t.Helper()
	root = setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeWorkflow(t, root, "sample2", strings.Replace(sampleWorkflow, "workflow: sample\n", "workflow: sample2\n", 1))
	writeContextFile(t, root, "notes.txt", "x\n")
	good = startStatusRun(t, "sample", "name=Ada")
	bad = startStatusRun(t, "sample2", "name=Bea")
	ref, err := journal.FindRun(root, bad)
	if err != nil {
		t.Fatal(err)
	}
	return root, good, bad, ref.Dir
}

func corruptPlan(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dropWorkflowPath keeps plan.json parseable but removes its recorded
// workflow path, which loadPinnedWorkflow rejects. (spec.Validate reports
// rule violations in its Report, not as an error, so a plan whose workflow
// merely violates rules is still a loadable, listed run.)
func dropWorkflowPath(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "plan.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	plan["workflow"].(map[string]any)["path"] = ""
	out, _ := json.Marshal(plan)
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertUnreadableEntry(t *testing.T, e map[string]any, id string) {
	t.Helper()
	if e["run_id"] != id || e["status"] != "unreadable" {
		t.Errorf("entry = %v, want unreadable %s", e, id)
	}
	if r, _ := e["reason"].(string); r == "" {
		t.Errorf("empty reason: %v", e)
	}
	if e["step"] != "" || e["attempt"] != float64(0) {
		t.Errorf("unknowable fields not zero: %v", e)
	}
}

func TestStatusJSON_UnreadablePlanDoesNotHideHealthyOnes(t *testing.T) {
	_, good, bad, dir := startBrokenPair(t)
	corruptPlan(t, dir)
	out, errOut, code := runCLI(t, []string{"pawl", "status", "--json"})
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d stderr %q, want 0 and empty", code, errOut)
	}
	runs := statusRuns(t, out)
	if len(runs) != 2 {
		t.Fatalf("runs = %v, want 2", runs)
	}
	byID := map[string]map[string]any{}
	for _, r := range runs {
		byID[r["run_id"].(string)] = r
	}
	if byID[good]["status"] != "running" {
		t.Errorf("healthy run = %v", byID[good])
	}
	assertUnreadableEntry(t, byID[bad], bad)
}

func TestStatusJSON_PinnedLoadFailureListedUnreadable(t *testing.T) {
	_, good, bad, dir := startBrokenPair(t)
	dropWorkflowPath(t, dir)
	out, errOut, code := runCLI(t, []string{"pawl", "status", "--json"})
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d stderr %q, want 0 and empty", code, errOut)
	}
	runs := statusRuns(t, out)
	if len(runs) != 2 {
		t.Fatalf("runs = %v, want 2", runs)
	}
	for _, r := range runs {
		if r["run_id"] == bad {
			assertUnreadableEntry(t, r, bad)
		} else if r["run_id"] != good {
			t.Errorf("unexpected run %v", r)
		}
	}
}

func TestStatus_RunFlagUnreadable(t *testing.T) {
	_, _, bad, dir := startBrokenPair(t)
	corruptPlan(t, dir)
	out, errOut, code := runCLI(t, []string{"pawl", "status", "--run", bad, "--json"})
	if code != 0 || errOut != "" {
		t.Fatalf("json: exit %d stderr %q", code, errOut)
	}
	runs := statusRuns(t, out)
	if len(runs) != 1 {
		t.Fatalf("runs = %v", runs)
	}
	assertUnreadableEntry(t, runs[0], bad)

	out, errOut, code = runCLI(t, []string{"pawl", "status", "--run", bad})
	if code != 0 || errOut != "" {
		t.Fatalf("text: exit %d stderr %q", code, errOut)
	}
	for _, want := range []string{"status: unreadable", "detail:", "run: " + bad} {
		if !strings.Contains(out, want) {
			t.Errorf("text output %q lacks %q", out, want)
		}
	}
}
