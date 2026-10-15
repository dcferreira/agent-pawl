package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// startSample starts the sample workflow and returns its run id.
func startSample(t *testing.T) (root, runID string) {
	t.Helper()
	root = setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "n\n")
	if _, e, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"}); code != 0 {
		t.Fatalf("run: %d %s", code, e)
	}
	live, err := journal.Live(root)
	if err != nil || len(live) != 1 {
		t.Fatalf("live = %v %v", live, err)
	}
	return root, live[0].RunID
}

// TestCommands_RepeatedFlagExitsUsage: a repeat is a usage error for every
// command that has flags.
func TestCommands_RepeatedFlagExitsUsage(t *testing.T) {
	setupWorkingCopy(t)
	for _, args := range [][]string{
		{"pawl", "run", "sample", "--fresh", "--fresh"},
		{"pawl", "run", "sample", "--run", "a", "--run=b"},
		{"pawl", "submit", "--run", "a", "--run", "b", "--step", "s", "--json", "{}"},
		{"pawl", "poll", "--run", "a", "--step", "s", "--step", "t"},
		{"pawl", "status", "--json", "--json"},
		{"pawl", "abandon", "--run", "a", "--run", "b"},
		{"pawl", "validate", "--path", "a", "--path=b"},
	} {
		_, stderr, code := runCLI(t, args)
		if code != 2 || !strings.Contains(stderr, "given more than once") || !strings.Contains(stderr, "Usage:") {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
	// update and version go through their own entry points.
	var o, e bytes.Buffer
	if code := CmdUpdate([]string{"--check", "--check"}, &o, &e, "dev", nil); code != 2 || !strings.Contains(e.String(), "--check given more than once") {
		t.Errorf("update repeat: %d %q", code, e.String())
	}
}

// TestCommands_HelpExitsZeroOnStdout: --help/-h, even next to bad args.
func TestCommands_HelpExitsZeroOnStdout(t *testing.T) {
	setupWorkingCopy(t)
	for _, cmd := range []string{"run", "submit", "poll", "status", "abandon", "validate", "list", "hook"} {
		for _, h := range []string{"--help", "-h"} {
			stdout, stderr, code := runCLI(t, []string{"pawl", cmd, "--bogus", h})
			if code != 0 || stderr != "" || !strings.Contains(stdout, "pawl "+cmd) || !strings.HasPrefix(stdout, "Usage:") {
				t.Errorf("%s %s: exit %d stdout %q stderr %q", cmd, h, code, stdout, stderr)
			}
		}
	}
	var o, e bytes.Buffer
	if code := CmdUpdate([]string{"--help"}, &o, &e, "dev", nil); code != 0 || e.Len() != 0 || !strings.Contains(o.String(), "pawl update") {
		t.Errorf("update --help: %d %q %q", code, o.String(), e.String())
	}
	o.Reset()
	if code := CmdVersion([]string{"-h"}, &o, &e, "dev"); code != 0 || !strings.Contains(o.String(), "pawl version") {
		t.Errorf("version -h: %d %q", code, o.String())
	}
	if code := CmdVersion([]string{"extra"}, &o, &e, "dev"); code != 2 {
		t.Errorf("version extra: %d", code)
	}
}

func TestTopLevelHelp(t *testing.T) {
	for _, a := range []string{"help", "--help", "-h"} {
		stdout, stderr, code := runCLI(t, []string{"pawl", a})
		if code != 0 || stderr != "" || !strings.Contains(stdout, "pawl run") || !strings.Contains(stdout, "pawl hook pre|stop|subagent-stop") {
			t.Errorf("%s: exit %d stdout %q stderr %q", a, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"pawl"}, {"pawl", "bogus"}} {
		stdout, stderr, code := runCLI(t, args)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "pawl run") {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}

func TestRun_EqualsFormFlags(t *testing.T) {
	_, runID := startSample(t)
	// --run=<id> resumes the live run.
	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "--run=" + runID})
	if code != 0 || !strings.Contains(stdout, "resume: run "+runID) {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestRun_BoolWithValueIsUsageError(t *testing.T) {
	setupWorkingCopy(t)
	_, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "--fresh=true"})
	if code != 2 || !strings.Contains(stderr, "--fresh does not take a value") {
		t.Errorf("exit %d stderr %q", code, stderr)
	}
}

func TestRun_NoNameIsUsageError(t *testing.T) {
	setupWorkingCopy(t)
	_, stderr, code := runCLI(t, []string{"pawl", "run", "--fresh"})
	if code != 2 || !strings.Contains(stderr, "missing workflow name") {
		t.Errorf("exit %d stderr %q", code, stderr)
	}
}

func TestSubmitPoll_EqualsFormParsed(t *testing.T) {
	setupWorkingCopy(t)
	// Parsed fine, so it gets as far as failing to find the run (exit 1),
	// not a usage error.
	_, stderr, code := runCLI(t, []string{"pawl", "submit", "--run=nope", "--step=s", "--json={}", "--item=0"})
	if code != 1 {
		t.Errorf("submit: exit %d stderr %q, want 1", code, stderr)
	}
	_, stderr, code = runCLI(t, []string{"pawl", "poll", "--run=nope", "--step=s"})
	if code != 1 {
		t.Errorf("poll: exit %d stderr %q, want 1", code, stderr)
	}
}

func TestStatusAbandon_EqualsForm(t *testing.T) {
	_, runID := startSample(t)
	if out, e, code := runCLI(t, []string{"pawl", "status", "--run=" + runID, "--json"}); code != 0 || !strings.Contains(out, runID) {
		t.Fatalf("status: %d %q %q", code, out, e)
	}
	out, e, code := runCLI(t, []string{"pawl", "abandon", "--run=" + runID, "--reason=because"})
	if code != 0 {
		t.Fatalf("abandon: %d %q %q", code, out, e)
	}
	if out, _, _ := runCLI(t, []string{"pawl", "status", "--run=" + runID}); !strings.Contains(out, "because") {
		t.Errorf("reason not recorded: %q", out)
	}
}

func TestValidate_EqualsForm(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
	if out, e, code := runCLI(t, []string{"pawl", "validate", "--path=.claude/workflows/sample.yaml"}); code != 0 || !strings.Contains(out, "workflow:") {
		t.Fatalf("%d %q %q", code, out, e)
	}
}

func TestValidate_ErrorsOnStderr(t *testing.T) {
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "broken", "workflow: broken\nstart: a\nsteps:\n  - id: a\n    kind: deterministic\n    run: echo hi\n")
	stdout, stderr, code := runCLI(t, []string{"pawl", "validate", "broken"})
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr, `step "a"`) || strings.Contains(stdout, `step "a"`) {
		t.Errorf("errors must be on stderr only: stdout %q stderr %q", stdout, stderr)
	}
	if !strings.Contains(stdout, "workflow:") {
		t.Errorf("workflow line must stay on stdout: %q", stdout)
	}
}

func TestList_ExtraArgsAreUsageErrors(t *testing.T) {
	setupWorkingCopy(t)
	for _, args := range [][]string{{"pawl", "list", "x"}, {"pawl", "list", "--x=1"}} {
		if _, _, code := runCLI(t, args); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestUpdate_EqualsForm(t *testing.T) {
	var o, e bytes.Buffer
	// The pin is validated before any network access; seeing the pin in the
	// message proves --version=<v> was parsed as a value.
	if code := CmdUpdate([]string{"--version=bogus"}, &o, &e, "dev", nil); code != 2 || !strings.Contains(e.String(), `"bogus"`) {
		t.Errorf("%d %q", code, e.String())
	}
	e.Reset()
	if code := CmdUpdate([]string{"--version="}, &o, &e, "dev", nil); code != 2 || !strings.Contains(e.String(), "--version needs a non-empty value") {
		t.Errorf("%d %q", code, e.String())
	}
}

func TestHook_UsageStaysFailOpen(t *testing.T) {
	setupWorkingCopy(t)
	_, stderr, code := runCLI(t, []string{"pawl", "hook"})
	if code != 1 || !strings.Contains(stderr, "pawl hook") {
		t.Errorf("exit %d stderr %q", code, stderr)
	}
	if _, _, code := runCLI(t, []string{"pawl", "hook", "pre", "extra"}); code != 1 {
		t.Errorf("exit %d", code)
	}
}
