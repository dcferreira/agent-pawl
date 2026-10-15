package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rule16Workflow = `workflow: r16
start: prep
steps:
  - id: prep
    kind: deterministic
    run: scripts/prep.sh
    next: ask
  - id: ask
    kind: agentic
    description: "do the thing"
    writes:
      out: {type: string}
    postcondition: {all_set: [out]}
    next: done
state:
  out:
    type: string
    default: ""
terminal:
  done: {status: ok, message: fine}
`

func writePrepScript(t *testing.T, root string) string {
	t.Helper()
	p := filepath.Join(root, ".claude", "workflows", "scripts", "prep.sh")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidate_Rule16MissingScriptFails(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "r16", rule16Workflow)
	_, stderr, code := runCLI(t, []string{"pawl", "validate", "r16"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, `step "prep": rule 16: run: "scripts/prep.sh"`) || !strings.Contains(stderr, "does not exist") {
		t.Errorf("stderr missing rule-16 error: %q", stderr)
	}
	wantPrefix := filepath.Join(root, ".claude", "workflows", "r16.yaml") + `: step "prep": rule 16: `
	if !strings.HasPrefix(stderr, wantPrefix) || !strings.Contains(stderr, "; create it there or fix the path") {
		t.Errorf("stderr is not the one-line rule shape (want prefix %q): %q", wantPrefix, stderr)
	}
	writePrepScript(t, root)
	if _, stderr, code := runCLI(t, []string{"pawl", "validate", "r16"}); code != 0 {
		t.Errorf("with the script present: exit %d, stderr %q", code, stderr)
	}
}

func TestRun_NewRunRule16MissingScriptRefuses(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "r16", rule16Workflow)
	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "r16"})
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d; stdout = %q stderr = %q", code, exitUsage, stdout, stderr)
	}
	if !strings.Contains(stderr, `step "prep": rule 16: run: "scripts/prep.sh"`) {
		t.Errorf("stderr missing rule-16 error: %q", stderr)
	}
	if strings.Contains(stdout, "DISPATCH") {
		t.Errorf("run started despite the missing script: %q", stdout)
	}
}

// A script deleted after its step already ran must not make the live run
// unresumable, nor break `pawl status`.
func TestRun_ResumeIgnoresRule16(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "r16", rule16Workflow)
	script := writePrepScript(t, root)

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "r16"})
	if code != 0 || !strings.Contains(stdout, "DISPATCH ") {
		t.Fatalf("first run: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}

	stdout2, stderr2, code2 := runCLI(t, []string{"pawl", "run", "r16"})
	if code2 != 0 {
		t.Fatalf("resume refused: exit %d stderr %q", code2, stderr2)
	}
	if !strings.Contains(stdout2, "resume: run ") {
		t.Errorf("not a resume: %q", stdout2)
	}
	if _, stderr3, code3 := runCLI(t, []string{"pawl", "status"}); code3 != 0 {
		t.Errorf("status: exit %d stderr %q", code3, stderr3)
	}
}
