package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

// TestDerivedSpecFields_NotInPlanOrDigest pins that load-derived spec fields
// (Step.Fields, StateDecl.DefaultSet, CatchRule.UnknownKeys) never reach
// plan.json and never influence Digest.
func TestDerivedSpecFields_NotInPlanOrDigest(t *testing.T) {
	const src = `workflow: derived
start: a
state:
  blob: {type: json, default: null}
steps:
  - id: a
    kind: deterministic
    run: "true"
    next: done
    catch: [{on: failure, next: done}]
terminal: {done: {status: ok}}
`
	path := filepath.Join(t.TempDir(), "wf.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := spec.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !w.State["blob"].DefaultSet || len(w.Steps[0].Fields) == 0 {
		t.Fatalf("fixture did not exercise derived fields: %+v / %v", w.State["blob"], w.Steps[0].Fields)
	}

	// (a) plan.json shape.
	data, err := json.Marshal(&Plan{Workflow: w})
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, bad := range []string{"DefaultSet", "UnknownKeys", `"Fields"`} {
		if strings.Contains(out, bad) {
			t.Errorf("plan JSON contains derived field %q: %s", bad, out)
		}
	}

	// (b) digest ignores derived fields.
	before, err := Digest(w)
	if err != nil {
		t.Fatal(err)
	}
	for i := range w.Steps {
		w.Steps[i].Fields = nil
		for j := range w.Steps[i].Catch {
			w.Steps[i].Catch[j].UnknownKeys = []string{"zzz"}
		}
	}
	for k, d := range w.State {
		d.DefaultSet = !d.DefaultSet
		w.State[k] = d
	}
	after, err := Digest(w)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("digest changed after mutating only derived fields: %s != %s", before, after)
	}
}
