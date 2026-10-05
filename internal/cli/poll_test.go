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
// to blocked. every:/timeout: are sub-second defaults so the real clock costs
// the tests nothing (tests that need other timing derive a variant via
// pollVariant). Note timeout: runs from step entry, so tests that must not
// see it expire override it with a generous value. cli has no clock to fake,
// deliberately: the injection point is engine.Engine's Now/Sleep, exercised in
// internal/engine's own poll tests.
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

// pollVariant returns pollWorkflow with the wait step's poll:, every: and
// timeout: replaced, so a test can pick the timing it needs without forking
// the whole workflow. It fails the test if any needle is not present exactly
// once in pollWorkflow, so a drifted pollWorkflow cannot silently leave the
// original timing in place.
func pollVariant(t *testing.T, pollCmd, every, timeout string) string {
	t.Helper()
	pairs := [][2]string{
		{"poll: cat status", "poll: " + pollCmd},
		{"every: 100ms", "every: " + every},
		{"timeout: 400ms", "timeout: " + timeout},
	}
	var args []string
	for _, p := range pairs {
		if n := strings.Count(pollWorkflow, p[0]); n != 1 {
			t.Fatalf("pollVariant: %q appears %d times in pollWorkflow, want exactly 1 (pollWorkflow changed?)", p[0], n)
		}
		args = append(args, p[0], p[1])
	}
	return strings.NewReplacer(args...).Replace(pollWorkflow)
}

// TestPoll_UnroutedKeepsPolling: a poll: that reports an unrouted token keeps
// the loop going (several visible iterations) until a routed token arrives.
// The script is a counter, not a clock: it prints PENDING (no route) on its
// first two invocations and PASSED on the third, so the outcome depends on
// invocation count alone. timeout: is generous because it is measured from
// step entry, not from when pawl poll starts — a short one could expire before
// the third poll on a slow runner and make this test flaky.
func TestPoll_UnroutedKeepsPolling(t *testing.T) {
	root := setupWorkingCopy(t)
	script := `n=$(cat count 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > count
if [ "$n" -lt 3 ]; then echo PENDING; else echo "PASSED build_status=PASSED"; fi
`
	if err := os.WriteFile(filepath.Join(root, "poll.sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWorkflow(t, root, "polly", pollVariant(t, "sh poll.sh", "10ms", "1m"))

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "polly"})
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %q", code, stderr)
	}
	runID := runIDFromWait(t, stdout)

	stdout, stderr, code = runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "wait_for_build"})
	if code != 0 {
		t.Fatalf("poll exit = %d, want 0 (routed to the agentic step); stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "poll 3") {
		t.Errorf("poll did not keep polling past the unrouted iterations:\n%s", stdout)
	}
	if !strings.Contains(stdout, "PENDING") {
		t.Errorf("poll did not log the unrouted token it saw:\n%s", stdout)
	}
	if !strings.Contains(stdout, "DISPATCH") || !strings.Contains(stdout, "summarize_build") {
		t.Errorf("poll did not dispatch summarize_build once the token routed:\n%s", stdout)
	}
}

// TestPoll_TimeoutRoutesToBlocked: once timeout: has elapsed since step entry,
// pawl poll routes the timeout outcome to the blocked terminal. The timeout is
// the smallest the validator accepts (any valid Go duration; it sets no
// minimum), so it has expired by the time poll runs. Deliberately NOT asserted:
// how many polls ran — expiry may legitimately precede the first poll.
func TestPoll_TimeoutRoutesToBlocked(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "polly", pollVariant(t, "cat status", "10ms", "1ms"))
	writeStatusFile(t, root, "PENDING")

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "polly"})
	if code != 0 {
		t.Fatalf("run exit = %d, stderr = %q", code, stderr)
	}
	runID := runIDFromWait(t, stdout)

	stdout, stderr, code = runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "wait_for_build"})
	if code != 3 {
		t.Fatalf("poll exit = %d, want 3 (BLOCKED terminal); stderr = %q", code, stderr)
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
