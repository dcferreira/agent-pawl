package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// authoringExerciseLabel is the exact line an unproven example's NOTES.md
// must carry. Keep it byte-for-byte in sync with the label line in each
// authoring exercise's NOTES.md.
const authoringExerciseLabel = "> **Authoring exercise, not run.** This workflow is a design sketch: it validates (`pawl validate`), but no test runs it and its external dependencies are not stubbed. See [../README.md](../README.md)."

// TestExamplesAreProvenOrLabelled makes every docs/examples directory either
// proven (an e2e/*_test.go file references it) or explicitly labelled an
// authoring exercise, never both and never neither, and requires
// docs/examples/README.md to list it.
func TestExamplesAreProvenOrLabelled(t *testing.T) {
	root := filepath.Join("..", "..")
	entries, err := os.ReadDir(filepath.Join(root, "docs", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	e2eFiles, err := filepath.Glob(filepath.Join(root, "e2e", "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	var e2eSrc []string
	for _, f := range e2eFiles {
		// docs_output_test.go copies example workflows only to capture
		// `pawl validate`/`pawl list` output for the docs; it never runs
		// them, so a reference there proves nothing.
		if filepath.Base(f) == "docs_output_test.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		e2eSrc = append(e2eSrc, string(b))
	}
	readme, err := os.ReadFile(filepath.Join(root, "docs", "examples", "README.md"))
	if err != nil {
		t.Fatalf("docs/examples/README.md must exist: %v", err)
	}

	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n++
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(root, "docs", "examples", name)
			if _, err := os.Stat(filepath.Join(dir, "workflow.yaml")); err != nil {
				t.Errorf("docs/examples/%s has no workflow.yaml", name)
			}
			// "docs/examples/<name>" followed by a non-name char (or end), so
			// foreach does not match foreach-fanout.
			re := regexp.MustCompile(regexp.QuoteMeta("docs/examples/"+name) + `($|[^A-Za-z0-9_-])`)
			proven := false
			for _, src := range e2eSrc {
				if re.MatchString(src) {
					proven = true
					break
				}
			}
			notes, _ := os.ReadFile(filepath.Join(dir, "NOTES.md"))
			labelled := strings.Contains(string(notes), authoringExerciseLabel)
			switch {
			case proven && labelled:
				t.Errorf("docs/examples/%s is covered by an e2e test but its NOTES.md still carries the authoring-exercise label; remove the label", name)
			case !proven && !labelled:
				t.Errorf("docs/examples/%s is neither referenced by an e2e/*_test.go file nor labelled an authoring exercise; add an e2e test, or add the label line (see authoringExerciseLabel in this file) near the top of its NOTES.md", name)
			}
			if !strings.Contains(string(readme), "| `"+name+"` |") {
				t.Errorf("docs/examples/README.md does not mention %q; add it to the table", name)
			}
		})
	}
	if n == 0 {
		t.Fatal("no example directories found; was docs/examples moved?")
	}
}
