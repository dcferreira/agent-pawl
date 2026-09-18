package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// loadWorkflow writes yaml to a temp file, loads and validates it, and fails
// the test if validation reports any error — mirroring the "pawl run gates on
// Validate returning zero errors" contract this package relies on.
func loadWorkflow(t *testing.T, yaml string) *spec.Workflow {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := spec.Load(path)
	if err != nil {
		t.Fatalf("spec.Load: %v", err)
	}
	report, err := spec.Validate(w)
	if err != nil {
		t.Fatalf("spec.Validate: %v", err)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("workflow has validation errors: %v\n---\n%s", report.Errors, yaml)
	}
	return w
}

// newTestEngine loads yaml, points PAWL_STATE_DIR at a fresh temp dir (so
// tests never touch the real state directory), and returns an Engine rooted
// at a separate temp "working copy" directory where run: scripts execute.
func newTestEngine(t *testing.T, yaml string) *Engine {
	t.Helper()
	w := loadWorkflow(t, yaml)
	t.Setenv(journal.EnvStateDir, t.TempDir())
	root := t.TempDir()
	e := New(w, root)
	e.Timeout = 5 * time.Second
	return e
}

// writeScript writes an executable shell script into e.Root/name.
func writeScript(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -e\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}
