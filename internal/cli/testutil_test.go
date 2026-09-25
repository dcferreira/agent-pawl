package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// runIDPattern matches newRunID's 4-hex-character output, wherever it
// appears in a command's printed output (the run id is random, so tests
// normalise it to a fixed placeholder before comparing against expected
// text).
var runIDPattern = regexp.MustCompile(`\b[0-9a-f]{4}\b`)

// setupWorkingCopy creates a fresh temp directory, chdirs into it (t.Chdir
// restores on cleanup), and points PAWL_STATE_DIR at a separate fresh temp
// directory so no test ever touches the real state directory. It returns
// the working copy root.
func setupWorkingCopy(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv(journal.EnvStateDir, t.TempDir())
	// The enforcement gate is exercised in enforce_test.go; every other
	// test runs with it explicitly off.
	t.Setenv("PAWL_ENFORCEMENT", "off")
	return root
}

// writeWorkflow writes yaml as .claude/workflows/<name>.yaml under root.
func writeWorkflow(t *testing.T, root, name, yaml string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeContextFile writes content next to a workflow file (context: entries
// resolve relative to the workflow file's own directory).
func writeContextFile(t *testing.T, root, name string, content string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "workflows")
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeExecutable writes an executable file directly under root — where a
// "!cmd" context entry resolves it from (execShell's cmd.Dir is e.Root, the
// working-copy root, unlike a plain context entry, which resolves relative
// to the workflow file's own directory).
func writeExecutable(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// normaliseRunID replaces every 4-hex-character run id in s with "RUNID",
// so a golden comparison does not depend on newRunID's random output.
func normaliseRunID(s string) string {
	return runIDPattern.ReplaceAllString(s, "RUNID")
}
