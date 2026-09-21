package cli

import (
	"strings"
	"testing"
)

// waitWorkflow is the CLI-level counterpart of internal/engine's: a
// deterministic step that pawl run executes itself, then a wait step it must
// park at, printing the WAIT line of DESIGN.md §2's handshake.
const waitWorkflow = `workflow: waity
start: kick_off
state:
  build_status: {type: string, default: ""}
steps:
  - id: kick_off
    kind: deterministic
    run: touch kicked
    next: wait_for_build
  - id: wait_for_build
    kind: wait
    poll: touch polled
    every: 5s
    timeout: 5m
    emits: pairs
    writes: {build_status: {type: string}}
    outcomes:
      PASSED: done
      FAILED: blocked
      timeout: blocked
terminal:
  done:    {status: ok,      message: "built"}
  blocked: {status: blocked, message: "paused: ${blocked_reason}"}
`

// TestRun_WaitBlock golden-file tests the WAIT block: the column-0 WAIT
// line, the poll command the /pawl skill is told to run, and the END WAIT
// sentinel that closes the block (DESIGN.md §2).
func TestRun_WaitBlock(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "waity", waitWorkflow)

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "waity"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	want := `WAIT RUNID wait_for_build
every: 5s
timeout: 5m
poll with: pawl poll --run RUNID --step wait_for_build
END WAIT RUNID wait_for_build
`
	got := normaliseRunID(stdout)
	if !strings.HasSuffix(got, want) {
		t.Errorf("stdout:\n%s\ndoes not end with:\n%s", got, want)
	}
}

// TestRun_WaitIsIdempotentOnRerun checks that pawl run against a run already
// parked at a wait step re-prints the same WAIT line rather than starting a
// second run or erroring.
func TestRun_WaitIsIdempotentOnRerun(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "waity", waitWorkflow)

	first, stderr, code := runCLI(t, []string{"pawl", "run", "waity"})
	if code != 0 {
		t.Fatalf("first run exit = %d, stderr = %q", code, stderr)
	}
	second, stderr, code := runCLI(t, []string{"pawl", "run", "waity"})
	if code != 0 {
		t.Fatalf("second run exit = %d, stderr = %q", code, stderr)
	}
	line := func(s string) string {
		for _, l := range strings.Split(normaliseRunID(s), "\n") {
			if strings.HasPrefix(l, "WAIT ") {
				return l
			}
		}
		return ""
	}
	if line(first) == "" {
		t.Fatalf("first run printed no WAIT line:\n%s", first)
	}
	if line(second) != line(first) {
		t.Errorf("re-run WAIT line = %q, want the same as %q", line(second), line(first))
	}
}

// TestSubmit_RefusedAgainstWaitStep pins DESIGN.md §3's rule at the CLI
// boundary: pawl submit against a wait-parked step is refused, naming pawl
// poll instead.
func TestSubmit_RefusedAgainstWaitStep(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "waity", waitWorkflow)

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "waity"})
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %q", code, stderr)
	}
	runID := ""
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "WAIT ") {
			runID = strings.Fields(l)[1]
		}
	}
	if runID == "" {
		t.Fatalf("no WAIT line in:\n%s", stdout)
	}

	_, stderr, code = runCLI(t, []string{"pawl", "submit", "--run", runID, "--step", "wait_for_build", "--json", `{"build_status":"PASSED"}`})
	if code == 0 {
		t.Fatal("pawl submit against a wait step succeeded; it must be refused")
	}
	if !strings.Contains(stderr, "pawl poll") {
		t.Errorf("refusal %q does not tell the caller to run pawl poll", stderr)
	}
}
