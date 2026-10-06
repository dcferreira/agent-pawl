package cli

import (
	"path/filepath"
	"testing"
)

// TestShippedWorkflowsValidate runs the load+validate path `pawl validate
// --path` uses over every docs/examples workflow, so one can't rot
// unnoticed (docs/examples/manage-mr once did). Warnings and the soft-step
// census are not failures; a load error or any report error is, exactly as in
// cmdValidate.
func TestShippedWorkflowsValidate(t *testing.T) {
	root := filepath.Join("..", "..")
	var files []string
	for _, pattern := range []string{
		"docs/examples/*/workflow.yaml",
	} {
		m, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		if len(m) == 0 {
			t.Fatalf("no files match %s; was the directory moved?", pattern)
		}
		files = append(files, m...)
	}
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			_, report, err := loadAndValidate(f)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			for _, e := range report.Errors {
				t.Error(e)
			}
		})
	}
}
