package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// TestSubmit_LockHeldExitsRefused pins docs/cli.md's exit-code table
// (~line 33): a run lock held by another live process is a refusal, exit 4
// — not a resolution error (1) and not an engine bug (5). It simulates a
// live holder the way journal.LockHolder itself checks liveness: a lock
// file naming this test process's own pid, which is unquestionably alive.
func TestSubmit_LockHeldExitsRefused(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")

	stdout, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH "))

	resolvedRoot, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	dir := journal.RunDir(resolvedRoot, "sample", runID)
	lockPath := filepath.Join(dir, "lock")
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatalf("planting a live-holder lock: %v", err)
	}

	_, stderr, code2 := runCLI(t, []string{"pawl", "submit", "--run", runID, "--step", "greet", "--json", `{"greeting":"hi"}`})
	if code2 != 4 {
		t.Fatalf("exit = %d, want 4 (lock held by a live process); stderr = %q", code2, stderr)
	}
	// Exact text (reviewer item 5): the message used to repeat itself —
	// "journal: run is locked by pid N: journal: run is locked by another
	// process" — because journal.ErrHeld's own sentinel text duplicated the
	// detail message it was %w-wrapped in front of. It must now read once.
	want := fmt.Sprintf("pawl submit: journal: run locked by pid %d (alive); wait, or use --force if that process is gone\n", os.Getpid())
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// TestSubmit_WrongStepExitsRefused pins the same table's "submit for a step
// that isn't the current one" refusal: submitting for a step the run is not
// actually waiting on is exit 4, distinct from a usage error (2) — the flags
// were all well-formed, the run just isn't waiting there.
func TestSubmit_WrongStepExitsRefused(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")

	stdout, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH "))

	_, stderr, code2 := runCLI(t, []string{"pawl", "submit", "--run", runID, "--step", "not-a-real-step", "--json", `{}`})
	if code2 != 4 {
		t.Fatalf("exit = %d, want 4 (submit for a step the run is not waiting on); stderr = %q", code2, stderr)
	}
	if !strings.Contains(stderr, "not-a-real-step") {
		t.Errorf("stderr = %q, want it to name the mismatched step", stderr)
	}
}

// TestSubmit_BrokenRunDirectoryExitsEngineError pins the exit-5 row: once
// findLiveRun has already confirmed a run exists and is live, a run
// directory whose plan.json cannot be read (corrupted, or written by a
// version of pawl this build no longer understands) is a broken run
// directory, not an unknown run — the caller can't fix this by retyping the
// command, which is exactly what separates exit 5 from exit 1.
func TestSubmit_BrokenRunDirectoryExitsEngineError(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")

	stdout, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH "))

	resolvedRoot, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	dir := journal.RunDir(resolvedRoot, "sample", runID)
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("corrupting plan.json: %v", err)
	}

	_, stderr, code2 := runCLI(t, []string{"pawl", "submit", "--run", runID, "--step", "greet", "--json", `{"greeting":"hi"}`})
	if code2 != 5 {
		t.Fatalf("exit = %d, want 5 (broken run directory); stderr = %q", code2, stderr)
	}
}

// finishSample drives sampleWorkflow's one agentic step to its TERMINAL ok
// end, returning the run id — a small helper for the terminal-run tests
// below (reviewer item 1: submit/abandon against an already-terminal run).
func finishSample(t *testing.T, root string) string {
	t.Helper()
	stdout, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH "))
	stdout2, _, code2 := runCLI(t, []string{"pawl", "submit", "--run", runID, "--step", "greet", "--json", `{"greeting":"hi"}`})
	if code2 != 0 || !strings.Contains(stdout2, "TERMINAL "+runID+" ok") {
		t.Fatalf("driving the run to TERMINAL ok: exit %d, stdout = %q", code2, stdout2)
	}
	return runID
}

// TestSubmit_TerminalRunExitsRefused is reviewer item 1: submitting against
// a run that has already finished must be exit 4 (a refusal — the run
// exists, it just isn't accepting submissions any more), not exit 1 ("no
// live run"). It used to be 1 because findLiveRun filtered terminal runs out
// before engine.Submit ever got a chance to return ErrAlreadyTerminal.
func TestSubmit_TerminalRunExitsRefused(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	runID := finishSample(t, root)

	_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", runID, "--step", "greet", "--json", `{"greeting":"hi"}`})
	if code != 4 {
		t.Fatalf("exit = %d, want 4 (run already finished); stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "already finished") {
		t.Errorf("stderr = %q, want it to say the run has already finished", stderr)
	}
}

// TestAbandon_TerminalRunExitsRefused is item 1's other command: pawl
// abandon has no engine call to discover this on its own (it appends
// RUN_END directly), so it must check ref.State.Terminal() itself before
// journalling a second RUN_END for a run that has already ended.
func TestAbandon_TerminalRunExitsRefused(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	runID := finishSample(t, root)

	_, stderr, code := runCLI(t, []string{"pawl", "abandon", "--run", runID})
	if code != 4 {
		t.Fatalf("exit = %d, want 4 (run already ended); stderr = %q", code, stderr)
	}
	want := fmt.Sprintf("pawl abandon: run %s has already ended (ok); nothing to abandon\n", runID)
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// TestSubmit_UnknownRunExitsResolution and TestAbandon_UnknownRunExitsResolution
// are item 1's other half: a run id that does not exist at all is still a
// resolution error (1), not a refusal — findRunByID must tell "no such run"
// apart from "that run exists but has ended" (which is each command's own
// exit-4 refusal above).
func TestSubmit_UnknownRunExitsResolution(t *testing.T) {
	setupWorkingCopy(t)
	_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run", "0000", "--step", "greet", "--json", `{}`})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (no such run); stderr = %q", code, stderr)
	}
}

func TestAbandon_UnknownRunExitsResolution(t *testing.T) {
	setupWorkingCopy(t)
	_, stderr, code := runCLI(t, []string{"pawl", "abandon", "--run", "0000"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (no such run); stderr = %q", code, stderr)
	}
}

// waityWorkflow is the CLI-level wait workflow shared by the poll exit-code
// tests below (a copy of wait_test.go's waitWorkflow so this file doesn't
// depend on test file load order for a package-level const).
const waityWorkflowForExitCodes = `workflow: waity2
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

// TestPoll_UnknownRunExitsResolution is item 2: a run id that never existed
// is a resolution error (1), distinct from the two "nothing to do, exit 0"
// cases below (a terminal run, or ErrPollNotCurrent) — pawl poll runs
// unattended under Monitor, so those two stay quiet, but a run id that was
// never going to resolve is the caller's own mistake, not an expected race.
func TestPoll_UnknownRunExitsResolution(t *testing.T) {
	setupWorkingCopy(t)
	_, stderr, code := runCLI(t, []string{"pawl", "poll", "--run", "0000", "--step", "wait_for_build"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (no such run); stderr = %q", code, stderr)
	}
}

// TestPoll_TerminalRunExitsQuietly is item 2's "stays 0" case: a run that
// existed and has already ended (here, abandoned) is the same expected race
// ErrPollNotCurrent covers — pawl poll must not turn "the run moved on"
// into a refusal just because it can now tell a never-existed run apart
// from an ended one.
func TestPoll_TerminalRunExitsQuietly(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "waity2", waityWorkflowForExitCodes)

	stdout, _, code := runCLI(t, []string{"pawl", "run", "waity2"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := runIDFromWait(t, stdout)
	if _, _, code := runCLI(t, []string{"pawl", "abandon", "--run", runID}); code != 0 {
		t.Fatalf("pawl abandon: exit %d", code)
	}

	stdout2, stderr2, code2 := runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "wait_for_build"})
	if code2 != 0 {
		t.Fatalf("exit = %d, want 0 (an expected race, not a failure); stdout = %q, stderr = %q", code2, stdout2, stderr2)
	}
	if !strings.Contains(stdout2, "already ended") {
		t.Errorf("stdout = %q, want it to explain the run already ended", stdout2)
	}
}

// TestPoll_CurrentStepNotWaitExitsRefused is item 3: polling a step that is
// current but is not kind: wait (here, an in-flight agentic DISPATCH) is a
// caller mistake distinct from ErrPollNotCurrent — a real step, currently
// live, that simply isn't the kind pawl poll drives — so it's a refusal
// (exit 4, engine.ErrRefused), not the quiet exit 0 a moved-on run gets.
func TestPoll_CurrentStepNotWaitExitsRefused(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")

	stdout, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH "))

	_, stderr, code2 := runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "greet"})
	if code2 != 4 {
		t.Fatalf("exit = %d, want 4 (not a wait step); stderr = %q", code2, stderr)
	}
	if !strings.Contains(stderr, "not a wait step") {
		t.Errorf("stderr = %q, want it to say the step is not a wait step", stderr)
	}
}

// TestPoll_ChangedWorkflowExitsRefused and TestPoll_BrokenRunDirectoryExitsEngineError
// are reviewer item 7's missing poll tests: the same workflow-changed (4)
// and broken-run-directory (5) refusals pawl submit already had tests for.
func TestPoll_ChangedWorkflowExitsRefused(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "waity2", waityWorkflowForExitCodes)

	stdout, _, code := runCLI(t, []string{"pawl", "run", "waity2"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := runIDFromWait(t, stdout)

	edited := strings.Replace(waityWorkflowForExitCodes, `message: "built"`, `message: "built differently"`, 1)
	writeWorkflow(t, root, "waity2", edited)

	_, stderr, code2 := runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "wait_for_build"})
	if code2 != 4 {
		t.Fatalf("exit = %d, want 4 (workflow changed); stderr = %q", code2, stderr)
	}
	if !strings.Contains(stderr, "changed") || !strings.Contains(stderr, "abandon") {
		t.Errorf("stderr = %q, want it to say the workflow changed and suggest abandon", stderr)
	}
}

func TestPoll_BrokenRunDirectoryExitsEngineError(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "waity2", waityWorkflowForExitCodes)

	stdout, _, code := runCLI(t, []string{"pawl", "run", "waity2"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := runIDFromWait(t, stdout)

	resolvedRoot, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	dir := journal.RunDir(resolvedRoot, "waity2", runID)
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("corrupting plan.json: %v", err)
	}

	_, stderr, code2 := runCLI(t, []string{"pawl", "poll", "--run", runID, "--step", "wait_for_build"})
	if code2 != 5 {
		t.Fatalf("exit = %d, want 5 (broken run directory); stderr = %q", code2, stderr)
	}
}

// TestAbandon_LockHeldExitsRefused and TestAbandon_BrokenRunDirectoryExitsEngineError
// are item 7's abandon cases.
func TestAbandon_LockHeldExitsRefused(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")

	stdout, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH "))

	resolvedRoot, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	dir := journal.RunDir(resolvedRoot, "sample", runID)
	if err := os.WriteFile(filepath.Join(dir, "lock"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatalf("planting a live-holder lock: %v", err)
	}

	_, stderr, code2 := runCLI(t, []string{"pawl", "abandon", "--run", runID})
	if code2 != 4 {
		t.Fatalf("exit = %d, want 4 (lock held); stderr = %q", code2, stderr)
	}
}

// TestAbandon_BrokenRunDirectoryExitsEngineError makes the run directory
// itself unwritable (not just its lock file) so AcquireLock's O_CREATE
// fails for a reason other than the lock already existing — a genuine I/O
// failure, not the lock-held refusal above — which must map to 5, not 4.
// Skipped under root, where permission bits don't block writes.
func TestAbandon_BrokenRunDirectoryExitsEngineError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits don't block directory writes")
	}
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")

	stdout, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH "))

	resolvedRoot, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	dir := journal.RunDir(resolvedRoot, "sample", runID)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, stderr, code2 := runCLI(t, []string{"pawl", "abandon", "--run", runID})
	if code2 != 5 {
		t.Fatalf("exit = %d, want 5 (broken run directory, not a lock refusal); stderr = %q", code2, stderr)
	}
}

// TestStatus_BrokenRunDirectoryExitsEngineError is item 7's status case.
func TestStatus_BrokenRunDirectoryExitsEngineError(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")

	stdout, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("pawl run: exit %d", code)
	}
	runID := extractRunID(t, firstLineHavingPrefix(t, stdout, "DISPATCH "))

	resolvedRoot, err := journal.ResolveRoot(root)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	dir := journal.RunDir(resolvedRoot, "sample", runID)
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("corrupting plan.json: %v", err)
	}

	_, stderr, code2 := runCLI(t, []string{"pawl", "status", "--run", runID})
	if code2 != 5 {
		t.Fatalf("exit = %d, want 5 (broken run directory); stderr = %q", code2, stderr)
	}
}

// firstLineHavingPrefix returns the first line of s with the given prefix,
// or fails the test — a small helper so exit-code tests don't each re-derive
// their own DISPATCH-line scan.
func firstLineHavingPrefix(t *testing.T, s, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no line with prefix %q in:\n%s", prefix, s)
	return ""
}
