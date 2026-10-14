package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// TestLoadPinnedWorkflow_IgnoresStoredDigestString: the pinned-vs-current
// comparison re-digests plan.Workflow with this build instead of trusting the
// digest string plan.json recorded, so a stale recorded string is not a
// "workflow changed" report when the file is unchanged.
func TestLoadPinnedWorkflow_IgnoresStoredDigestString(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "waity2", waityWorkflowForExitCodes)
	stdout, _, code := runCLI(t, []string{"pawl", "run", "waity2"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := runIDFromWait(t, stdout)
	resolvedRoot, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := journal.RunDir(resolvedRoot, "waity2", runID)

	planPath := filepath.Join(dir, "plan.json")
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`"digest": "[0-9a-f]+"`)
	if !re.Match(data) {
		t.Fatalf("no digest in plan.json: %s", data)
	}
	if err := os.WriteFile(planPath, re.ReplaceAll(data, []byte(`"digest": "stale"`)), 0o644); err != nil {
		t.Fatal(err)
	}

	pw, err := loadPinnedWorkflow(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pw.Changed {
		t.Errorf("Changed = true for an unchanged workflow with a stale recorded digest (detail %q)", pw.Detail)
	}
}
