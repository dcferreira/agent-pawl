package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

func writeRawPlan(t *testing.T, raw string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestReadPlan_LegacyFormat1DecodesToLoadedWorkflow decodes plan.json files
// written by a format-1 pawl (Go field names; see testdata/legacy) and checks
// each yields the same Workflow as spec.Load of the YAML it came from. A
// multi-word field (MaxStepsRaw...) that was not renamed would be silently
// zero-filled by encoding/json and show up as a difference here.
func TestReadPlan_LegacyFormat1DecodesToLoadedWorkflow(t *testing.T) {
	for _, name := range []string{"full", "unknowns"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/legacy/" + name + ".plan.format1.json")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := ReadPlan(writeRawPlan(t, string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			if plan.Format != 1 {
				t.Errorf("Format = %d, want 1 (normalised from missing)", plan.Format)
			}
			want, err := spec.Load("testdata/legacy/" + name + ".yaml")
			if err != nil {
				t.Fatal(err)
			}
			want.Path = "/legacy/" + name + ".yaml"

			gotJSON, _ := json.Marshal(plan.Workflow)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("legacy decode differs from Load\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
			gd, _ := Digest(plan.Workflow)
			wd, _ := Digest(want)
			if gd != wd {
				t.Errorf("digest differs: %s vs %s", gd, wd)
			}
		})
	}
}

// TestReadPlan_LegacyKeepsUserDataKeys: map keys and interface values are
// user data and must never be renamed, even when they spell a Go field name.
func TestReadPlan_LegacyKeepsUserDataKeys(t *testing.T) {
	const raw = `{
  "workflow": {
    "Workflow": "w", "Start": "s", "MaxStepsRaw": 7, "MaxSteps": 7,
    "Args": {"Path": {"Type": "string", "Default": {"MaxStepsRaw": 1, "Path": "x"}, "Required": true}},
    "State": {"Attempts": {"Type": "string", "Default": "", "MaxLength": 5}},
    "Steps": [{
      "ID": "s", "Kind": "agentic",
      "SubagentArgs": {"Path": "p", "Attempts": 3, "MaxStepsRaw": {"Path": 1}},
      "Writes": {"Keys": ["Attempts"], "Types": {"Attempts": "string"}, "MaxLength": {"MaxStepsRaw": 4}, "IsTyped": true},
      "Postcondition": {"Equals": {"Path": "v", "Attempts": {"MaxStepsRaw": 2}}, "HasEquals": true},
      "AttemptsRaw": 2, "Attempts": 2,
      "Outcomes": {"Path": "done", "Attempts": "done", "MaxStepsRaw": "done"}
    }],
    "Terminal": {"Path": {"Status": "ok", "Message": "m"}, "done": {"Status": "ok", "Message": "m"}},
    "Path": "/x/workflow.yaml"
  },
  "digest": "old"
}`
	plan, err := ReadPlan(writeRawPlan(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	w := plan.Workflow
	if w.MaxStepsRaw == nil || *w.MaxStepsRaw != 7 || w.Path != "/x/workflow.yaml" {
		t.Errorf("workflow scalars lost: %+v", w)
	}
	if _, ok := w.Args["Path"]; !ok {
		t.Errorf("arg key Path renamed: %v", w.Args)
	}
	if d := w.Args["Path"].Default; d == nil || !reflect.DeepEqual(*d, map[string]any{"MaxStepsRaw": 1.0, "Path": "x"}) {
		t.Errorf("arg default was walked: %v", d)
	}
	if sd, ok := w.State["Attempts"]; !ok || sd.MaxLength != 5 {
		t.Errorf("state key Attempts renamed or MaxLength lost: %v", w.State)
	}
	if _, ok := w.Terminal["Path"]; !ok {
		t.Errorf("terminal key Path renamed: %v", w.Terminal)
	}
	s := w.Steps[0]
	if want := (map[string]any{"Path": "p", "Attempts": 3.0, "MaxStepsRaw": map[string]any{"Path": 1.0}}); !reflect.DeepEqual(s.SubagentArgs, want) {
		t.Errorf("SubagentArgs = %#v", s.SubagentArgs)
	}
	if want := (map[string]string{"Path": "done", "Attempts": "done", "MaxStepsRaw": "done"}); !reflect.DeepEqual(s.Outcomes, want) {
		t.Errorf("Outcomes = %v", s.Outcomes)
	}
	if s.Writes.Types["Attempts"] != "string" || s.Writes.MaxLength["MaxStepsRaw"] != 4 || !s.Writes.IsTyped {
		t.Errorf("Writes = %+v", s.Writes)
	}
	if want := (map[string]any{"Path": "v", "Attempts": map[string]any{"MaxStepsRaw": 2.0}}); !reflect.DeepEqual(s.Postcondition.Equals, want) || !s.Postcondition.HasEquals {
		t.Errorf("Equals = %#v", s.Postcondition)
	}
	if s.AttemptsRaw == nil || *s.AttemptsRaw != 2 || s.Attempts != 2 {
		t.Errorf("step attempts lost: %+v", s)
	}
}
