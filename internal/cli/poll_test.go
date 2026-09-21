package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pollWorkflow drives pawl poll end to end at the CLI boundary: a wait step
// whose poll: reads a status file in the working copy, routing PASSED to an
// agentic step (so the poller must print a DISPATCH, not run it) and timeout
// to blocked. every:/timeout: are sub-second so the real clock costs the test
// nothing — cli has no clock to fake, deliberately: the injection point is
// engine.Engine's Now/Sleep, exercised in internal/engine's own poll tests.
const pollWorkflow = `workflow: polly
start: kick_off
state:
  build_status: {type: string, default: ""}
  summary:      {type: string, default: ""}
steps:
  - id: kick_off
    kind: deterministic
    run: "true"
    next: wait_for_build
  - id: wait_for_build
    kind: wait
    poll: cat status
    every: 100ms
    timeout: 400ms
    emits: pairs
    writes: {build_status: {type: string}}
    outcomes:
      PASSED: summarize_build
      FAILED: blocked
      timeout: blocked
  - id: summarize_build
    kind: agentic
    description: "summarise ${build_status}"
    writes: {summary: {type: string}}
    postcondition: {all_set: [summary]}
    next: done
terminal:
  done:    {status: ok,      message: "built ${summary}"}
  blocked: {status: blocked, message: "paused: ${blocked_reason}"}
`

func writeStatusFile(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "status"), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runIDFromWait pulls the run id off the WAIT line pawl run printed.
func runIDFromWait(t *testing.T, stdout string) string {
	t.Helper()
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "WAIT ") {
			return strings.Fields(l)[1]
		}
	}
	t.Fatalf("no WAIT line in:\n%s", stdout)
	return ""
}

// TestPoll_RoutedTokenPrintsNextInstruction: pawl poll submits for itself and
// prints the DISPATCH for the agentic step the routed outcome leads to — it
// must not run the agentic step itself.
func TestPoll_RoutedTokenPrintsNextInstruction(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "polly", pollWorkflow)
	writeStatusFile(t, root, "PASSED build_status=PASSED")

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "polly"})
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %q", code, stderr)
	}
	runID := runIDFromWait(t, stdout)

	stdout, stderr, code = runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "wait_for_build"})
	if code != 0 {
		t.Fatalf("poll exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "\nDISPATCH ") && !strings.HasPrefix(stdout, "DISPATCH ") {
		t.Errorf("poll printed no DISPATCH line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "summarize_build") {
		t.Errorf("poll did not dispatch summarize_build:\n%s", stdout)
	}
	// Per-iteration visibility: the poll loop is not silent.
	if !strings.Contains(stdout, "poll 1") {
		t.Errorf("poll printed no per-iteration log line:\n%s", stdout)
	}
}

// TestPoll_UnroutedThenTimeout: a status file that never reports keeps the
// loop going (several visible iterations) and then routes timeout to blocked.
func TestPoll_UnroutedThenTimeout(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "polly", pollWorkflow)
	writeStatusFile(t, root, "PENDING")

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "polly"})
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %q", code, stderr)
	}
	runID := runIDFromWait(t, stdout)

	stdout, stderr, code = runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "wait_for_build"})
	if code != 0 {
		t.Fatalf("poll exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "poll 2") {
		t.Errorf("poll did not keep polling past the first unrouted iteration:\n%s", stdout)
	}
	if !strings.Contains(stdout, "PENDING") {
		t.Errorf("poll did not log the unrouted token it saw:\n%s", stdout)
	}
	if !strings.Contains(stdout, "TERMINAL "+runID+" blocked") {
		t.Errorf("poll did not reach the timeout route's blocked terminal:\n%s", stdout)
	}
}

// TestPoll_NotCurrentStepExitsQuietly: a poll for a step the run has already
// left is an expected race (another process advanced it), not an error — it
// exits 0, having done nothing, and says so.
func TestPoll_NotCurrentStepExitsQuietly(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "polly", pollWorkflow)
	writeStatusFile(t, root, "PENDING")

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "polly"})
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %q", code, stderr)
	}
	runID := runIDFromWait(t, stdout)

	stdout, stderr, code = runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "kick_off"})
	if code != 0 {
		t.Fatalf("poll exit = %d, want 0 (an expected race, not a failure); stderr = %q", code, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "kick_off") || !strings.Contains(strings.ToLower(combined), "no longer") {
		t.Errorf("poll did not explain the early exit:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

// TestPoll_UsageErrors: the flags are required, like pawl submit's.
func TestPoll_UsageErrors(t *testing.T) {
	setupWorkingCopy(t)
	_, stderr, code := runCLI(t, []string{"pawl", "poll", "--run", "abcd"})
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "pawl poll --run") {
		t.Errorf("stderr = %q, want a usage line", stderr)
	}
}
