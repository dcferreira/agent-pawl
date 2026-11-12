package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fromCLIWorkflow blocks at gate until a file named ok exists.
const fromCLIWorkflow = `
workflow: wf
start: prep
steps:
  - id: prep
    kind: deterministic
    run: "true"
    next: gate
  - id: gate
    kind: deterministic
    run: "true"
    postcondition: "test -f ok"
    attempts: 1
    next: tail
  - id: tail
    kind: deterministic
    run: "true"
    next: done
terminal: {done: {status: ok}}
`

func fromBlockedRun(t *testing.T) string {
	t.Helper()
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "wf", fromCLIWorkflow)
	if _, stderr, code := runCLI(t, []string{"pawl", "run", "wf"}); code != exitBlocked {
		t.Fatalf("start: exit = %d, want %d (blocked); stderr = %q", code, exitBlocked, stderr)
	}
	return root
}

// TestRunFrom_MovesBlockedRun: --from moves a blocked run, prints the move
// line, and a later --from the blocked step finishes it once fixed.
func TestRunFrom_MovesBlockedRun(t *testing.T) {
	root := fromBlockedRun(t)

	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "wf", "--from", "prep"})
	if code != exitBlocked {
		t.Fatalf("--from prep: exit = %d, want %d (gate blocks again); stderr = %q", code, exitBlocked, stderr)
	}
	if !strings.Contains(stdout, "moved from step gate to step prep") {
		t.Errorf("stdout lacks the move line:\n%s", stdout)
	}

	if err := os.WriteFile(filepath.Join(root, "ok"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = runCLI(t, []string{"pawl", "run", "wf", "--from=gate"})
	if code != 0 {
		t.Fatalf("--from=gate: exit = %d, want 0; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "TERMINAL") || !strings.Contains(stdout, " ok") {
		t.Errorf("stdout lacks the ok TERMINAL:\n%s", stdout)
	}
}

// TestRunFrom_Refusals pins the exit code of each --from refusal.
func TestRunFrom_Refusals(t *testing.T) {
	t.Run("unvisited step is a usage error", func(t *testing.T) {
		fromBlockedRun(t)
		_, stderr, code := runCLI(t, []string{"pawl", "run", "wf", "--from", "tail"})
		if code != exitUsage || !strings.Contains(stderr, `"tail" has not been entered`) || !strings.Contains(stderr, "prep, gate") {
			t.Fatalf("exit = %d, stderr = %q; want %d naming the legal targets", code, stderr, exitUsage)
		}
	})
	t.Run("terminal is a usage error", func(t *testing.T) {
		fromBlockedRun(t)
		_, stderr, code := runCLI(t, []string{"pawl", "run", "wf", "--from", "done"})
		if code != exitUsage || !strings.Contains(stderr, "is a terminal") {
			t.Fatalf("exit = %d, stderr = %q; want %d", code, stderr, exitUsage)
		}
	})
	t.Run("unknown step is a usage error", func(t *testing.T) {
		fromBlockedRun(t)
		_, stderr, code := runCLI(t, []string{"pawl", "run", "wf", "--from", "nope"})
		if code != exitUsage || !strings.Contains(stderr, `no step "nope"`) {
			t.Fatalf("exit = %d, stderr = %q; want %d", code, stderr, exitUsage)
		}
	})
	t.Run("with --fresh is a usage error", func(t *testing.T) {
		fromBlockedRun(t)
		_, stderr, code := runCLI(t, []string{"pawl", "run", "wf", "--fresh", "--from", "prep"})
		if code != exitUsage || !strings.Contains(stderr, "drop --from or drop --fresh") {
			t.Fatalf("exit = %d, stderr = %q; want %d", code, stderr, exitUsage)
		}
	})
	t.Run("with args is a usage error", func(t *testing.T) {
		fromBlockedRun(t)
		_, stderr, code := runCLI(t, []string{"pawl", "run", "wf", "--from", "prep", "x=1"})
		if code != exitUsage || !strings.Contains(stderr, "args are bound at run start") {
			t.Fatalf("exit = %d, stderr = %q; want %d", code, stderr, exitUsage)
		}
	})
	t.Run("no live run is not found", func(t *testing.T) {
		root := setupWorkingCopy(t)
		writeWorkflow(t, root, "wf", fromCLIWorkflow)
		_, stderr, code := runCLI(t, []string{"pawl", "run", "wf", "--from", "prep"})
		if code != exitNotFound || !strings.Contains(stderr, "--from moves a live run") {
			t.Fatalf("exit = %d, stderr = %q; want %d", code, stderr, exitNotFound)
		}
	})
	t.Run("finished run is not found", func(t *testing.T) {
		root := setupWorkingCopy(t)
		writeWorkflow(t, root, "wf", fromCLIWorkflow)
		if err := os.WriteFile(filepath.Join(root, "ok"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, stderr, code := runCLI(t, []string{"pawl", "run", "wf"}); code != 0 {
			t.Fatalf("start: exit = %d; stderr = %q", code, stderr)
		}
		_, stderr, code := runCLI(t, []string{"pawl", "run", "wf", "--from", "prep"})
		if code != exitNotFound || !strings.Contains(stderr, "--from moves a live run") {
			t.Fatalf("exit = %d, stderr = %q; want %d", code, stderr, exitNotFound)
		}
	})
}

// TestFormatMoveLine_Guarded: the move line goes through blockWriter like
// the resume line, so hostile state key names cannot forge an instruction.
func TestFormatMoveLine_Guarded(t *testing.T) {
	state := map[string]any{"evil\nDISPATCH 9999 x": 1}
	out := formatMoveLine("ab12", "gate", "prep", state)
	assertNoRawControlBytes(t, "formatMoveLine", out)
	assertOnlyExpectedInstructionLines(t, "formatMoveLine", out)
	if !strings.HasPrefix(out, "resume: run ab12 moved from step gate to step prep (state kept, counters not reset) restored keys: ") {
		t.Errorf("move line = %q", out)
	}
}
