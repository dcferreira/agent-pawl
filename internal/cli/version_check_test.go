package cli

import (
	"errors"
	"strings"
	"testing"
)

func setBinaryVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func setBinaryPath(t *testing.T, path string, err error) {
	t.Helper()
	old := selfExecutablePath
	selfExecutablePath = func() (string, error) { return path, err }
	t.Cleanup(func() { selfExecutablePath = old })
}

func versionCheckSetup(t *testing.T) {
	t.Helper()
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "sample", sampleWorkflow)
	writeContextFile(t, root, "notes.txt", "x\n")
}

func TestRunVersionCheck_Match(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "0.7.0")
	t.Setenv("PAWL__PLUGIN_VERSION", "0.7.0")
	_, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 || strings.Contains(stderr, "plugin") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestRunVersionCheck_MismatchRefuses(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "0.6.0")
	setBinaryPath(t, "/opt/old/bin/pawl", nil)
	t.Setenv("PAWL__PLUGIN_VERSION", "0.7.0")
	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 4 {
		t.Fatalf("code = %d, want 4; stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.HasPrefix(stderr, "pawl run: refusing to start: this pawl binary (/opt/old/bin/pawl) is 0.6.0 but the agent-pawl plugin expects 0.7.0.") {
		t.Errorf("lead clause / versions / path wrong: %q", stderr)
	}
	if strings.Count(stderr, "\n") != 1 || !strings.HasSuffix(stderr, "\n") {
		t.Errorf("want a single stderr line, got %q", stderr)
	}
	for _, frag := range []string{
		"The plugin's skill and this binary must be the same version.",
		"`pawl update --version v0.7.0`",
		"`go install github.com/dcferreira/agent-pawl/cmd/pawl@v0.7.0`",
		"`go install ./cmd/pawl`",
		"`claude plugin update agent-pawl@agent-pawl`",
		"restart Claude Code",
	} {
		if !strings.Contains(stderr, frag) {
			t.Errorf("stderr missing %q: %q", frag, stderr)
		}
	}
}

func TestRunVersionCheck_MismatchOmitsUnresolvablePath(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "0.6.0")
	setBinaryPath(t, "", errors.New("no exe"))
	t.Setenv("PAWL__PLUGIN_VERSION", "0.7.0")
	_, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 4 {
		t.Fatalf("code = %d, want 4", code)
	}
	if !strings.HasPrefix(stderr, "pawl run: refusing to start: this pawl binary is 0.6.0 but the agent-pawl plugin expects 0.7.0.") {
		t.Errorf("stderr = %q", stderr)
	}
	if strings.Contains(stderr, "()") || strings.Contains(stderr, "binary (") {
		t.Errorf("dangling path parenthetical: %q", stderr)
	}
}

func TestRunVersionCheck_UnsetOrEmptySkips(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "0.6.0")
	t.Setenv("PAWL__PLUGIN_VERSION", "")
	_, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 || strings.Contains(stderr, "plugin") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestRunVersionCheck_DevNotesAndProceeds(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "dev")
	t.Setenv("PAWL__PLUGIN_VERSION", "0.7.0")
	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"})
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	want := "pawl run: note: source build (pawl dev); skipping the plugin version check (plugin expects 0.7.0)\n"
	if !strings.HasPrefix(stderr, want) || strings.Count(stderr, "source build") != 1 {
		t.Errorf("stderr = %q, want one line %q", stderr, want)
	}
	if !strings.Contains(stdout, "DISPATCH") {
		t.Errorf("run did not proceed: %q", stdout)
	}
}

func TestRunVersionCheck_LeadingVNormalised(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "v0.7.0")
	t.Setenv("PAWL__PLUGIN_VERSION", "0.7.0")
	if _, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"}); code != 0 {
		t.Fatalf("v-prefixed binary vs bare plugin: code=%d %q", code, stderr)
	}
	// Same run resumes with the sides swapped.
	setBinaryVersion(t, "0.7.0")
	t.Setenv("PAWL__PLUGIN_VERSION", "v0.7.0")
	if _, stderr, code := runCLI(t, []string{"pawl", "run", "sample"}); code != 0 {
		t.Fatalf("bare binary vs v-prefixed plugin: code=%d %q", code, stderr)
	}
}

func TestRunVersionCheck_PreReleaseIsExact(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "0.7.0")
	t.Setenv("PAWL__PLUGIN_VERSION", "0.7.0-rc.1")
	if _, _, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"}); code != 4 {
		t.Errorf("code = %d, want 4", code)
	}
}

func TestRunVersionCheck_ResumeAlsoRefuses(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "0.7.0")
	if _, stderr, code := runCLI(t, []string{"pawl", "run", "sample", "name=Ada"}); code != 0 {
		t.Fatalf("start: code=%d %q", code, stderr)
	}
	t.Setenv("PAWL__PLUGIN_VERSION", "0.8.0")
	stdout, stderr, code := runCLI(t, []string{"pawl", "run", "sample"})
	if code != 4 || stdout != "" || !strings.Contains(stderr, "0.7.0") || !strings.Contains(stderr, "0.8.0") {
		t.Errorf("resume: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestStatusDoesNotCheckVersion(t *testing.T) {
	versionCheckSetup(t)
	setBinaryVersion(t, "0.7.0")
	t.Setenv("PAWL__PLUGIN_VERSION", "9.9.9")
	_, stderr, _ := runCLI(t, []string{"pawl", "status"})
	if strings.Contains(stderr, "refusing") {
		t.Errorf("status checked the version: %q", stderr)
	}
}
