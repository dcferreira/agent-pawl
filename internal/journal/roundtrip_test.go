package journal

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	aj, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(aj) == string(bj)
}

// repoRoot is two levels above this package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestPlanRoundTripPreservesDigest: for every workflow YAML in the repo that
// spec.Load accepts, writing it to plan.json and reading it back yields a
// workflow with the same digest. This is the fidelity the re-digest-on-compare
// resume check depends on: anything the plan round-trip drops or mutates
// would read as "workflow changed".
func TestPlanRoundTripPreservesDigest(t *testing.T) {
	root := repoRoot(t)
	var files []string
	for _, base := range []string{"docs/examples", "testdata", "internal", ".claude/workflows"} {
		_ = filepath.WalkDir(filepath.Join(root, base), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml") {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(files) < 10 {
		t.Fatalf("found only %d workflow files; the walk is broken", len(files))
	}
	checked := 0
	for _, f := range files {
		w, err := spec.Load(f)
		if err != nil {
			continue // not a loadable workflow (error-case fixtures)
		}
		rel, _ := filepath.Rel(root, f)
		t.Run(rel, func(t *testing.T) {
			before, err := Digest(w)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if _, err := WritePlan(dir, w); err != nil {
				t.Fatal(err)
			}
			p, err := ReadPlan(dir)
			if err != nil {
				t.Fatal(err)
			}
			after, err := Digest(p.Workflow)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Errorf("digest changed across the plan.json round trip")
			}
			if p.Digest != before {
				t.Errorf("recorded digest %s != Digest(w) %s", p.Digest, before)
			}
		})
		checked++
	}
	if checked < 10 {
		t.Fatalf("only %d workflows loaded; expected the examples and fixtures", checked)
	}
}

// TestPlanRoundTripPreservesLargeIntegers: an integer above 2^53 in an
// interface-typed field must survive plan.json unchanged (decoded as
// json.Number, not float64), or resume reads it as "workflow changed".
func TestPlanRoundTripPreservesLargeIntegers(t *testing.T) {
	const big = int64(9007199254740993) // 2^53 + 1
	w := &spec.Workflow{
		Steps: []spec.Step{{
			ID:           "a",
			SubagentArgs: map[string]any{"n": big},
			Postcondition: &spec.Postcondition{
				Equals: map[string]any{"k": big}, HasEquals: true,
			},
		}},
	}
	before, err := Digest(w)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := WritePlan(dir, w); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPlan(dir)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Digest(p.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("digest changed across the plan.json round trip for an integer above 2^53")
	}
}
