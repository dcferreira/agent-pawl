package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// newWaitForBuildProject lays "docs/examples/wait-for-build" out the way pawl
// resolves a named workflow (YAML at .claude/workflows/<name>.yaml, scripts/
// alongside it). The example's status file path is relative to the run cwd
// (the project root), so it lands inside the temp dir.
func newWaitForBuildProject(t *testing.T) *project {
	t.Helper()
	src := filepath.Join(repoRoot(t), filepath.FromSlash("docs/examples/wait-for-build"))
	dir := t.TempDir()
	wf := filepath.Join(dir, ".claude", "workflows")
	copyFile(t, filepath.Join(src, "workflow.yaml"), mustMkdirFile(t, wf, "wait-for-build.yaml"), 0o644)
	entries, err := os.ReadDir(filepath.Join(src, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		copyFile(t, filepath.Join(src, "scripts", e.Name()), mustMkdirFile(t, filepath.Join(wf, "scripts"), e.Name()), info.Mode().Perm())
	}
	return &project{root: dir, stateDir: t.TempDir()}
}

// runWithEnv is project.run with extra environment variables.
func (p *project) runWithEnv(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(wfBinary(t), args...)
	cmd.Dir = p.root
	cmd.Env = append(os.Environ(), append([]string{"PAWL_STATE_DIR=" + p.stateDir, "PAWL_ENFORCEMENT=off"}, env...)...)
	out, err := cmd.Output()
	combined := string(out)
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		combined += string(ee.Stderr)
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running pawl %v: %v", args, err)
	}
	return combined, code
}

var waitRunID = regexp.MustCompile(`(?m)^WAIT\s+(\S+)\s+wait_for_build`)

// startWaitForBuild runs the example to its WAIT block and returns the run id.
// The simulated build sleeps SIMULATE_BUILD_SECONDS=1 instead of 17.
func startWaitForBuild(t *testing.T, p *project, env []string, args ...string) string {
	t.Helper()
	out, code := p.runWithEnv(t, env, append([]string{"run", "wait-for-build"}, args...)...)
	if code != 0 {
		t.Fatalf("pawl run wait-for-build: exit %d\n%s", code, out)
	}
	m := waitRunID.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("expected a WAIT block for wait_for_build, got:\n%s", out)
	}
	return m[1]
}

// TestWaitForBuildPassed drives docs/examples/wait-for-build on the PASSED
// route. kick_off_build and wait_for_build run for real (the "build" is
// scripts/simulate_build.sh sleeping 1s); only the agentic summarize_build
// step is a CANNED submit — no live subagent or LLM ran.
func TestWaitForBuildPassed(t *testing.T) {
	p := newWaitForBuildProject(t)
	env := []string{"SIMULATE_BUILD_SECONDS=1"}
	run := startWaitForBuild(t, p, env)

	out, code := p.runWithEnv(t, env, "poll", "--run", run, "--step", "wait_for_build")
	if code != 0 {
		t.Fatalf("pawl poll: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "DISPATCH "+run+" summarize_build") || !strings.Contains(out, "finished with status") || !strings.Contains(out, "PASSED") {
		t.Fatalf("expected a summarize_build DISPATCH carrying build_status PASSED, got:\n%s", out)
	}

	// CANNED submit: the test supplies the summary; no subagent ran.
	out, code = p.run(t, "submit", "--run", run, "--step", "summarize_build",
		"--json", `{"summary":"Canned summary: the build passed."}`)
	if code != 0 {
		t.Fatalf("pawl submit: exit %d\n%s", code, out)
	}
	if m := terminalLine.FindStringSubmatch(out); m == nil || m[1] != "ok" {
		t.Fatalf("expected TERMINAL ok, got:\n%s", out)
	}
	if !strings.Contains(out, "Build passed. Summary: Canned summary: the build passed.") {
		t.Fatalf("terminal message missing the summary:\n%s", out)
	}
}

// TestWaitForBuildFailed forces the FAILED route: the poll resolves FAILED and
// the run blocks. No agentic step is reached, so nothing is canned.
func TestWaitForBuildFailed(t *testing.T) {
	p := newWaitForBuildProject(t)
	env := []string{"SIMULATE_BUILD_SECONDS=1"}
	run := startWaitForBuild(t, p, env, "force_result=FAILED")

	out, code := p.runWithEnv(t, env, "poll", "--run", run, "--step", "wait_for_build")
	if code != 3 {
		t.Fatalf("pawl poll: exit %d, want 3 (blocked)\n%s", code, out)
	}
	if m := terminalLine.FindStringSubmatch(out); m == nil || m[1] != "blocked" {
		t.Fatalf("expected TERMINAL blocked, got:\n%s", out)
	}
	if strings.Contains(out, "summarize_build") {
		t.Fatalf("FAILED route must not reach summarize_build:\n%s", out)
	}
}
