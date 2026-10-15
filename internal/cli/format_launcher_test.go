package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/engine"
)

// launcherFile makes a real regular file (path with a space, to prove the
// quoting keeps it one token) and points PAWL__LAUNCHER at it.
func launcherFile(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my plugin", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "pawl")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envLauncher, p)
	return p
}

func printedInstructions() string {
	item := 1
	return formatDispatch(engine.Dispatch{RunID: "r1", Step: "s1"}, 1) +
		formatDispatchParallel(engine.DispatchParallel{Agentic: []engine.Dispatch{
			{RunID: "r1", Step: "b1"}, {RunID: "r1", Step: "b2", Item: &item}}}, map[string]int{}) +
		formatWait(engine.Wait{RunID: "r1", Step: "w1", Every: "5s", Timeout: "5m"}) +
		formatAsk(engine.Ask{RunID: "r1", Step: "a1", Options: []string{"x", "y"}}) +
		formatAsk(engine.Ask{RunID: "r1", Step: "a2", Multi: true, Options: []string{"x"}}) +
		formatAsk(engine.Ask{RunID: "r1", Step: "a3"})
}

func TestLauncherCommand_UsedInSubmitAndPollLines(t *testing.T) {
	p := launcherFile(t)
	got := printedInstructions()
	quoted := "'" + p + "'"
	for _, l := range strings.Split(got, "\n") {
		trim := strings.TrimSpace(l)
		for _, pre := range []string{"submit with: ", "or: ", "poll with: "} {
			if !strings.HasPrefix(trim, pre) {
				continue
			}
			if !strings.HasPrefix(strings.TrimPrefix(trim, pre), quoted+" ") {
				t.Errorf("line does not run the quoted launcher: %q", l)
			}
		}
	}
	if n := strings.Count(got, quoted+" submit"); n != 7 {
		t.Errorf("want 7 submit lines (dispatch, 2 branches, 2 ask+or, multi, free text) through the launcher, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, quoted+" poll"); n != 1 {
		t.Errorf("poll lines = %d", n)
	}
	if strings.Contains(got, "pawl submit") || strings.Contains(got, "pawl poll") {
		t.Errorf("plain pawl leaked:\n%s", got)
	}
}

func TestLauncherCommand_FallsBackToPlainPawl(t *testing.T) {
	dir := t.TempDir()
	for name, v := range map[string]string{
		"unset":       "",
		"relative":    "bin/pawl",
		"nonexistent": filepath.Join(dir, "nope"),
		"directory":   dir,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envLauncher, v)
			got := printedInstructions()
			if strings.Contains(got, dir) || strings.Contains(got, "bin/pawl") {
				t.Errorf("launcher used for %s:\n%s", name, got)
			}
			if n := strings.Count(got, "pawl submit"); n != 7 {
				t.Errorf("want plain 'pawl submit' x7, got %d", n)
			}
			if !strings.Contains(got, "poll with: pawl poll --run r1 --step w1") {
				t.Errorf("plain poll line missing:\n%s", got)
			}
		})
	}
}

func TestLauncherCommand_TerminalLinesStayPlain(t *testing.T) {
	launcherFile(t)
	got := formatTerminal(engine.Terminal{RunID: "r1", Status: "blocked"}, "", resumeHint{})
	if strings.Contains(got, "my plugin") {
		t.Errorf("terminal output must not carry the launcher path:\n%s", got)
	}
}
