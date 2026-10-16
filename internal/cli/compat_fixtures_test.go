package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

// TestCompatFixturesValidate validates the frozen workflow fixtures under
// testdata/compat/<major.minor>/<name>/. Unlike TestShippedWorkflowsValidate,
// which tracks the live files as they evolve, these are byte-for-byte copies
// of what a released tag shipped and are never edited, so a failure here means
// a later 1.x change broke a workflow that used to validate.
//
// Only errors fail the test, never warnings: a 1.x release may add warnings
// (additive), and only a new error is a break. Rule 16 (spec.CheckFiles)
// covers script existence and the execute bit, so this test does not repeat
// that check.
//
// Each fixture directory must hold exactly one top-level *.yaml, so a fixture
// can't be skipped because its workflow file has an unexpected name.
func TestCompatFixturesValidate(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "compat")
	dirs, err := filepath.Glob(filepath.Join(root, "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []string
	for _, d := range dirs {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		ys, err := filepath.Glob(filepath.Join(d, "*.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if len(ys) != 1 {
			t.Errorf("%s: want exactly one top-level *.yaml, found %d: %v", d, len(ys), ys)
			continue
		}
		fixtures = append(fixtures, ys[0])
	}
	if len(fixtures) == 0 {
		t.Fatalf("no fixtures match %s/*/*/*.yaml; was the directory moved?", root)
	}
	for _, f := range fixtures {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			w, report, err := loadAndValidate(f)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			for _, e := range report.Errors {
				t.Error(e)
			}
			for _, e := range spec.CheckFiles(w) {
				t.Error(e)
			}
		})
	}
}
