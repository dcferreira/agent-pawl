// Package e2e drives the real pawl binary (never internal/engine or
// internal/cli directly) against the docs/examples/green-tests workflow and the
// testdata/fixture Go module, to prove — with no LLM and no network — that
// spec, render, emit, journal, engine and cli compose into a working
// engine. The agentic fix_tests step is satisfied by scripted `pawl submit`
// calls; the fixture's "fix" is applied by this test writing new source
// directly, exactly as a real fix would land on disk before submission
// (design/format-spec.md §B.8: "the engine never touches the working
// tree").
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRootDir is the module root two levels up from this file
// (<repoRootDir>/e2e/green_tests_test.go), computed without a *testing.T so
// TestMain can call it too.
func repoRootDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("e2e: could not determine test file path")
	}
	return filepath.Dir(filepath.Dir(file)), nil
}

// repoRoot is repoRootDir, failing the test on error.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRootDir()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// binPath is the pawl binary TestMain builds once, before any test runs, and
// removes once every test has finished.
var binPath string

// TestMain builds cmd/pawl exactly once into its own temp directory, shared
// read-only by every test in this package. runTests does the real work
// inside a deferred-cleanup scope, since os.Exit itself must be the last
// thing TestMain does (os.Exit bypasses any defer still pending in the
// function that calls it).
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	root, err := repoRootDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dir, err := os.MkdirTemp("", "pawl-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: creating build dir:", err)
		return 1
	}
	defer os.RemoveAll(dir)

	binPath = filepath.Join(dir, "pawl")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/pawl")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: go build ./cmd/pawl: %v\n%s", err, out)
		return 1
	}

	return m.Run()
}

// wfBinary returns the path to the binary TestMain already built.
func wfBinary(t *testing.T) string {
	t.Helper()
	if binPath == "" {
		t.Fatal("e2e: pawl binary was not built (TestMain did not run?)")
	}
	return binPath
}

// project is a fixture project directory wired up as a working copy for
// pawl run/submit: the fixture Go module at its root, the green-tests
// workflow under .claude/workflows/, and its own isolated PAWL_STATE_DIR so
// the test never touches the real state directory.
type project struct {
	root      string
	stateDir  string
	fixtureGo string // path to fixture.go, the file under test
}

// newProject copies testdata/fixture and docs/examples/green-tests into a fresh
// temp directory, laid out the way resolveWorkflowFile and DESIGN.md §9's
// "scripts/ resolve relative to the workflow file" rule expect: the fixture
// Go module at root (cwd for run:, per §3, is unaffected by this layout),
// the workflow at .claude/workflows/green-tests.yaml, and its scripts/
// alongside it at .claude/workflows/scripts/ — exactly as docs/examples/
// green-tests itself lays scripts/ next to workflow.yaml.
func newProject(t *testing.T) *project {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()

	copyFile(t, filepath.Join(root, "testdata", "fixture", "go.mod"), filepath.Join(dir, "go.mod"), 0o644)
	copyFile(t, filepath.Join(root, "testdata", "fixture", "fixture.go"), filepath.Join(dir, "fixture.go"), 0o644)
	copyFile(t, filepath.Join(root, "testdata", "fixture", "fixture_test.go"), filepath.Join(dir, "fixture_test.go"), 0o644)

	if err := os.MkdirAll(filepath.Join(dir, ".claude", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(root, "docs", "examples", "green-tests", "workflow.yaml"), filepath.Join(dir, ".claude", "workflows", "green-tests.yaml"), 0o644)

	if err := os.MkdirAll(filepath.Join(dir, ".claude", "workflows", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(root, "docs", "examples", "green-tests", "scripts", "run-tests.sh"), filepath.Join(dir, ".claude", "workflows", "scripts", "run-tests.sh"), 0o755)

	return &project{
		root:      dir,
		stateDir:  t.TempDir(),
		fixtureGo: filepath.Join(dir, "fixture.go"),
	}
}

func copyFile(t *testing.T, src, dst string, perm os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, perm); err != nil {
		t.Fatalf("writing %s: %v", dst, err)
	}
}

// run invokes the built pawl binary with args, cwd at p.root and
// PAWL_STATE_DIR pointed at p.stateDir, and returns combined stdout+stderr
// and the exit code.
func (p *project) run(t *testing.T, args ...string) (output string, exitCode int) {
	t.Helper()
	cmd := exec.Command(wfBinary(t), args...)
	cmd.Dir = p.root
	cmd.Env = append(os.Environ(), "PAWL_STATE_DIR="+p.stateDir, "PAWL_ENFORCEMENT=off")
	out, err := cmd.Output()
	combined := string(out)
	if ee, ok := err.(*exec.ExitError); ok {
		combined += string(ee.Stderr)
		exitCode = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running pawl %v: %v", args, err)
	}
	return combined, exitCode
}

// brokenBuild writes syntactically invalid Go, so any postcondition or test
// run against it fails to compile.
func (p *project) brokenBuild(t *testing.T) {
	t.Helper()
	writeGo(t, p.fixtureGo, `package fixture

func Add(a, b int) int {
	return a + b
`)
}

// compilesButStillWrong writes syntactically valid Go that still has the
// original bug: the build succeeds, but TestAdd keeps failing.
func (p *project) compilesButStillWrong(t *testing.T) {
	t.Helper()
	writeGo(t, p.fixtureGo, `package fixture

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a - b
}
`)
}

// realFix writes the actual correct implementation.
func (p *project) realFix(t *testing.T) {
	t.Helper()
	writeGo(t, p.fixtureGo, `package fixture

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}
`)
}

func writeGo(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var dispatchRunID = regexp.MustCompile(`(?m)^DISPATCH\s+(\S+)\s+(\S+)`)

// extractRunID pulls the run id out of a DISPATCH or TERMINAL header line.
func extractRunID(t *testing.T, output string) string {
	t.Helper()
	if m := dispatchRunID.FindStringSubmatch(output); m != nil {
		return m[1]
	}
	t.Fatalf("no DISPATCH line found in output:\n%s", output)
	return ""
}

// TestGreenTestsEndToEnd drives the real pawl binary through the green-tests
// example against the fixture module, with the agentic fix_tests step
// satisfied entirely by scripted pawl submit calls that patch the fixture's
// source directly — no LLM, no network. It asserts the five behaviours the
// example exists to demonstrate (design/format-spec.md §B.4's two
// independent counters: attempts: on fix_tests' cheap build postcondition,
// max_visits: on run_tests re-observing the real suite).
func TestGreenTestsEndToEnd(t *testing.T) {
	p := newProject(t)

	// 1. The first `pawl run` reaches DISPATCH: the fixture starts with a
	// real bug (Add subtracts), so run_tests fails and routes to fix_tests.
	out, code := p.run(t, "run", "green-tests")
	if code != 0 {
		t.Fatalf("pawl run green-tests: exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "DISPATCH") || !strings.Contains(out, "fix_tests") {
		t.Fatalf("expected a DISPATCH of fix_tests, got:\n%s", out)
	}
	if !strings.Contains(out, "attempt: 1 of 3") {
		t.Fatalf("expected the first dispatch to be attempt 1 of 3, got:\n%s", out)
	}
	runID := extractRunID(t, out)

	// 2. A submit whose "fix" leaves the build broken burns an attempt and
	// re-dispatches with the previous failure text in the block.
	p.brokenBuild(t)
	out, code = p.run(t, "submit", "--run", runID, "--step", "fix_tests",
		"--json", `{"fix_summary":"introduced a syntax error"}`)
	if code != 0 {
		t.Fatalf("pawl submit (broken build): exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "DISPATCH") || !strings.Contains(out, "fix_tests") {
		t.Fatalf("expected a re-dispatch of fix_tests, got:\n%s", out)
	}
	if !strings.Contains(out, "attempt: 2 of 3") {
		t.Fatalf("expected the retry to burn an attempt (2 of 3), got:\n%s", out)
	}
	if !strings.Contains(out, "previous attempt failed") {
		t.Fatalf("expected the previous attempt's failure text in the block, got:\n%s", out)
	}
	if !strings.Contains(out, "syntax error") {
		t.Fatalf("expected the actual build failure text (syntax error) carried into the block, got:\n%s", out)
	}

	// 3. A submit that fixes the build but not the tests loops back through
	// run_tests: the postcondition (build_cmd) now passes, so fix_tests
	// routes to its next: run_tests, which re-runs the suite, still fails,
	// and dispatches fix_tests fresh (attempts reset to 1).
	p.compilesButStillWrong(t)
	out, code = p.run(t, "submit", "--run", runID, "--step", "fix_tests",
		"--json", `{"fix_summary":"fixed the syntax error only"}`)
	if code != 0 {
		t.Fatalf("pawl submit (build ok, tests still red): exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "DISPATCH") || !strings.Contains(out, "fix_tests") {
		t.Fatalf("expected another dispatch of fix_tests after looping through run_tests, got:\n%s", out)
	}
	if !strings.Contains(out, "attempt: 1 of 3") {
		t.Fatalf("expected the attempts budget to have reset on the fresh visit, got:\n%s", out)
	}
	statusOut, code := p.run(t, "status", "--run", runID)
	if code != 0 {
		t.Fatalf("pawl status: exit %d, output:\n%s", code, statusOut)
	}
	if !strings.Contains(statusOut, "run_tests=2") {
		t.Fatalf("expected pawl status to show run_tests visited twice (proving the loop re-entered it), got:\n%s", statusOut)
	}

	// 4. A submit that fixes both reaches TERMINAL … ok.
	p.realFix(t)
	out, code = p.run(t, "submit", "--run", runID, "--step", "fix_tests",
		"--json", `{"fix_summary":"fixed the actual bug"}`)
	if code != 0 {
		t.Fatalf("pawl submit (real fix): exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "TERMINAL "+runID+" ok") {
		t.Fatalf("expected TERMINAL %s ok, got:\n%s", runID, out)
	}
	// run_tests was entered 3 times (visit 1: FAIL; visit 2, after the
	// build-only fix: FAIL; visit 3, after the real fix: PASS) — the
	// terminal message's ${visits} must reflect that real count, not the
	// hardcoded 0 fix round 1 found in afterTransition.
	if !strings.Contains(out, "Suite green after 3 runs.") {
		t.Fatalf("expected the terminal message to report the real run_tests visit count (3), got:\n%s", out)
	}
}

// TestGreenTestsMaxVisitsGivesUp drives a second, independent run where the
// fix_tests step always leaves the tree compiling (so its cheap
// postcondition passes and no attempts: budget is ever burned) but never
// actually fixes the bug — so run_tests keeps failing every time it is
// re-entered. This exercises the *other* counter: run_tests' max_visits: 4
// caps the cycle independently of fix_tests' own attempts: budget, and
// routes the reserved `exhausted` outcome to the gave_up terminal.
func TestGreenTestsMaxVisitsGivesUp(t *testing.T) {
	p := newProject(t)

	out, code := p.run(t, "run", "green-tests")
	if code != 0 {
		t.Fatalf("pawl run green-tests: exit %d, output:\n%s", code, out)
	}
	runID := extractRunID(t, out)
	if !strings.Contains(out, "attempt: 1 of 3") {
		t.Fatalf("expected the first dispatch to be attempt 1 of 3, got:\n%s", out)
	}

	// run_tests has already been entered once (visit 1, FAIL). Three more
	// no-op "fixes" (build always compiles, bug never touched) drive
	// run_tests to its max_visits: 4 cap. Each round's postcondition
	// (build_cmd) passes trivially — nothing here ever burns an attempts:
	// retry — so every one of these dispatches must still read
	// "attempt: 1 of 3", the test's stated design point that the two
	// counters (attempts: on fix_tests, max_visits: on run_tests) are
	// independent: visits climbs to the cap while attempts never moves.
	for i := 0; i < 3; i++ {
		out, code = p.run(t, "submit", "--run", runID, "--step", "fix_tests",
			"--json", `{"fix_summary":"compiles, does not fix anything"}`)
		if code != 0 {
			t.Fatalf("pawl submit (round %d): exit %d, output:\n%s", i, code, out)
		}
		if !strings.Contains(out, "DISPATCH") {
			t.Fatalf("round %d: expected a re-dispatch (run should not have given up yet), got:\n%s", i, out)
		}
		if !strings.Contains(out, "attempt: 1 of 3") {
			t.Fatalf("round %d: expected attempt: 1 of 3 (attempts: never burned by a passing postcondition), got:\n%s", i, out)
		}
	}

	// The fourth submit re-enters run_tests for what would be its 5th
	// visit, past max_visits: 4: exhausted, routed to gave_up (blocked).
	out, code = p.run(t, "submit", "--run", runID, "--step", "fix_tests",
		"--json", `{"fix_summary":"compiles, does not fix anything"}`)
	if code != 3 {
		t.Fatalf("pawl submit (final round): exit %d, want 3 (BLOCKED terminal), output:\n%s", code, out)
	}
	if !strings.Contains(out, "TERMINAL "+runID+" blocked") {
		t.Fatalf("expected TERMINAL %s blocked (gave_up via max_visits), got:\n%s", runID, out)
	}
	if !strings.Contains(out, "exhausted") {
		t.Fatalf("expected the blocked reason to name the exhausted outcome, got:\n%s", out)
	}
}
