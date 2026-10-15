package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
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
		"no bundled binary",
	} {
		if !strings.Contains(stderr, frag) {
			t.Errorf("stderr missing %q: %q", frag, stderr)
		}
	}
	for _, frag := range []string{"terminal-installed", "claude plugin update", "hooks"} {
		if strings.Contains(stderr, frag) {
			t.Errorf("env-only message must keep the narrowed wording, found %q: %q", frag, stderr)
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

// Heartbeat-authority matrix. enforcedWC turns enforcement on; the hooks'
// plugin version rides on the heartbeat.

func hbVersionSetup(t *testing.T, bin string) string {
	t.Helper()
	root := enforcedWC(t)
	setBinaryVersion(t, bin)
	setBinaryPath(t, "/opt/old/bin/pawl", nil)
	return root
}

func TestRunVersionCheck_HeartbeatOnlyMismatchRefuses(t *testing.T) {
	root := hbVersionSetup(t, "0.8.0")
	if err := journal.WriteHeartbeatWithPlugin(root, "s1", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runPawl("run", "hookwf")
	if code != 4 || out != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	if !strings.HasPrefix(errs, "pawl run: refusing to start: this pawl binary (/opt/old/bin/pawl) is 0.8.0 but the agent-pawl plugin expects 0.9.0.") {
		t.Errorf("err=%q", errs)
	}
	for _, frag := range []string{
		"plugin hooks active in this session",
		"not the plugin's own binary",
		"terminal-installed pawl earlier on PATH",
		"stale plugin directory inherited on PATH",
		"Update or remove that binary: `pawl update --version v0.9.0` for an install.sh install, or `go install github.com/dcferreira/agent-pawl/cmd/pawl@v0.9.0`; or, if the plugin is the older side, run `claude plugin update agent-pawl@agent-pawl` and restart Claude Code.",
		"`claude plugin update agent-pawl@agent-pawl` and restart Claude Code",
	} {
		if !strings.Contains(errs, frag) {
			t.Errorf("heartbeat-only message missing %q: %q", frag, errs)
		}
	}
	if strings.Contains(errs, "no bundled binary") || strings.Contains(errs, "(hooks:") {
		t.Errorf("heartbeat-only message carries another case's text: %q", errs)
	}
	if live, _ := journal.Live(root); len(live) != 0 {
		t.Error("a refused run must not start")
	}
}

func TestRunVersionCheck_HeartbeatOnlyMatchOK(t *testing.T) {
	root := hbVersionSetup(t, "0.9.0")
	if err := journal.WriteHeartbeatWithPlugin(root, "s1", "v0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestRunVersionCheck_EnvOnlyEnforcementOffUnchanged(t *testing.T) {
	root := hbVersionSetup(t, "0.8.0")
	t.Setenv("PAWL_ENFORCEMENT", "off")
	// a heartbeat that would refuse must be ignored when enforcement is off
	if err := journal.WriteHeartbeatWithPlugin(root, "s1", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envPluginVersion, "0.8.0")
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	t.Setenv(envPluginVersion, "0.7.0")
	code, _, errs := runPawl("run", "hookwf", "--fresh")
	if code != 4 || !strings.Contains(errs, "expects 0.7.0.") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if strings.Contains(errs, "hooks:") {
		t.Errorf("env-only refusal must not name sources: %q", errs)
	}
}

func TestRunVersionCheck_BothAgreeOK(t *testing.T) {
	root := hbVersionSetup(t, "0.9.0")
	t.Setenv(envPluginVersion, "0.9.0")
	if err := journal.WriteHeartbeatWithPlugin(root, "s1", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestRunVersionCheck_BothDisagreeHeartbeatWinsNamesBoth(t *testing.T) {
	root := hbVersionSetup(t, "0.8.0")
	t.Setenv(envPluginVersion, "0.8.0") // env matches the binary; hooks do not
	if err := journal.WriteHeartbeatWithPlugin(root, "s1", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runPawl("run", "hookwf")
	if code != 4 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if !strings.HasPrefix(errs, "pawl run: refusing to start: this pawl binary (/opt/old/bin/pawl) is 0.8.0 but the agent-pawl plugin expects 0.9.0") {
		t.Errorf("err=%q", errs)
	}
	for _, frag := range []string{"(hooks: 0.9.0, launcher env: 0.8.0)", "Restart Claude Code."} {
		if !strings.Contains(errs, frag) {
			t.Errorf("missing %q: %q", frag, errs)
		}
	}
	for _, frag := range []string{"Fix the binary on PATH", "pawl update", "go install", "claude plugin update"} {
		if strings.Contains(errs, frag) {
			t.Errorf("both-disagree must not carry binary remediation %q: %q", frag, errs)
		}
	}
}

func TestRunVersionCheck_OtherWorkingCopyOrSessionHeartbeatNotUsed(t *testing.T) {
	root := hbVersionSetup(t, "0.8.0")
	// A different working copy's heartbeat, and another session's, both
	// carrying a mismatching version; this run's own fresh heartbeat has none.
	other := t.TempDir()
	if err := journal.WriteHeartbeatWithPlugin(other, "sx", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteSessionHeartbeatWithPlugin("other-session", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestRunVersionCheck_SessionHeartbeatPreferred(t *testing.T) {
	root := hbVersionSetup(t, "0.8.0")
	t.Setenv(envSessionID, "mine")
	if err := journal.WriteHeartbeatWithPlugin(root, "s1", "0.8.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteSessionHeartbeatWithPlugin("mine", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runPawl("run", "hookwf"); code != 4 || !strings.Contains(errs, "expects 0.9.0") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestRunVersionCheck_HeartbeatWithoutPluginVersionFallsBackToEnv(t *testing.T) {
	root := hbVersionSetup(t, "0.8.0")
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envPluginVersion, "0.7.0")
	code, _, errs := runPawl("run", "hookwf")
	if code != 4 || !strings.Contains(errs, "expects 0.7.0.") || strings.Contains(errs, "hooks:") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestRunVersionCheck_HeartbeatDevSkips(t *testing.T) {
	root := hbVersionSetup(t, "dev")
	if err := journal.WriteHeartbeatWithPlugin(root, "s1", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runPawl("run", "hookwf")
	if code != 0 || !strings.Contains(errs, "skipping the plugin version check (plugin expects 0.9.0)") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

// The version refusal must name the stale binary before any workflow is
// resolved or validated: a workflow using a field this older binary does not
// know would otherwise surface as a spec error about a correct workflow.
func TestRunVersionCheck_PrecedesWorkflowValidation(t *testing.T) {
	root := hbVersionSetup(t, "0.8.0")
	writeWorkflow(t, root, "badwf", "this_field_is_unknown: true\n")
	if err := journal.WriteHeartbeatWithPlugin(root, "s1", "0.9.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"badwf", "nosuchwf"} {
		code, out, errs := runPawl("run", name)
		if code != 4 || out != "" || !strings.Contains(errs, "expects 0.9.0") {
			t.Fatalf("%s: code=%d out=%q err=%q", name, code, out, errs)
		}
	}
}
