package e2e

// Golden test for the CLI output blocks in docs/**/*.md.
//
// Every fenced block that shows `pawl` output is introduced by a marker line
//
//	<!-- pawl-capture: <scenario-id> -->
//
// and the fence must start on the very next line. The block's content is not
// written by hand: it is the transcript of the scenario with that id in the
// table below, produced by running the real pawl binary (the one TestMain
// built) in a throwaway working copy. Run
//
//	UPDATE_DOCS=1 go test ./e2e -run TestDocsOutput
//
// to rewrite the blocks in place after a deliberate output change, then
// review the diff.
//
// Transcript format (one convention, used by every block): each visible
// command is shown as "$ <command line>", followed by its stdout and stderr
// exactly as printed (interleaved, as a terminal would show them), followed by
// "exit N" on its own line when N is non-zero. Commands with hidden set are
// run but not shown (setup, or the plugin's own PreToolUse hook call).
//
// Enforcement: scenarios with enforced set run without PAWL_ENFORCEMENT=off,
// after a real `pawl hook pre` call for a "pawl run" Bash command wrote the
// PreToolUse heartbeat — the same handshake a Claude Code session with the
// plugin performs before every pawl command (see TestEnforcedRun_*). Their
// banners therefore carry the real `hooks: PreToolUse ✔ (heartbeat)` line.

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docCmd is one step of a scenario.
type docCmd struct {
	args   []string          // pawl arguments; "{run}" is replaced by the scenario's first run id
	sh     string            // run via `sh -c` instead of pawl (shown as-is)
	stdin  string            // piped to pawl's stdin; "{project}" is the project directory
	show   string            // overrides the displayed command line
	hidden bool              // run, but leave out of the transcript
	dir    string            // working directory relative to the project root
	env    []string          // extra KEY=VALUE, after the scenario's own
	write  map[string]string // project-relative files written before the command
	exit   int               // expected exit status (default 0); a mismatch fails the test
	locked bool              // hold the run's lock, as a live process would, during the command
}

// docScenario is a working copy plus the commands run in it.
type docScenario struct {
	files    map[string]string // project-relative path -> content (.sh files are executable)
	copies   map[string]string // project-relative path -> repo-relative source file
	homeCopy map[string]string // HOME-relative path -> repo-relative source file
	path     map[string]string // extra executables placed first on PATH: name -> script body
	enforced bool              // see the package comment
	gitRepo  bool              // make the project a real git repository with the copied files committed
	noVCS    bool              // leave out the .git marker, so the working-copy root is cwd
	cmds     []docCmd
}

func pawlCmd(args ...string) docCmd { return docCmd{args: args} }
func hidden(c docCmd) docCmd        { c.hidden = true; return c }
func exits(n int, c docCmd) docCmd  { c.exit = n; return c }

const helloWorkflow = `workflow: hello
start: greet
args:
  name: {type: string, required: true}
steps:
  - id: greet
    kind: deterministic
    run: echo hello from ${name} > hello.txt
    postcondition: test -s hello.txt
    next: count
  - id: count
    kind: deterministic
    run: wc -l < hello.txt
    postcondition: test -f hello.txt
    next: done
terminal: {done: {status: ok, message: "Wrote hello.txt for ${name}."}}
`

// helloBlocked is helloWorkflow with greet's postcondition broken.
var helloBlocked = strings.Replace(helloWorkflow, "test -s hello.txt", "test -s nothing.txt", 1)

const oneStepBlocked = `workflow: hello
start: greet
args:
  name: {type: string, required: true}
steps:
  - id: greet
    kind: deterministic
    run: echo hello from ${name} > hello.txt
    postcondition: test -s nothing.txt
    next: done
terminal: {done: {status: ok, message: "Wrote hello.txt for ${name}."}}
`

const sampleWorkflow = `workflow: sample
start: greet
args:
  name: {type: string, required: true}
state:
  greeting: {type: string, default: ""}
steps:
  - id: greet
    kind: agentic
    description: Write a one-line greeting for ${name}.
    writes: {greeting: {type: string}}
    subagent_args: {model: haiku, tools: [Read]}
    postcondition: {all_set: [greeting]}
    next: done
terminal: {done: {status: ok, message: "Greeted ${name}."}}
`

const tidyWorkflow = `workflow: tidy
start: format
steps:
  - id: format
    kind: deterministic
    run: ruff format .
    postcondition: ruff format --check .
    next: test
  - id: test
    kind: deterministic
    run: pytest -q
    postcondition: pytest -q
    next: done
terminal: {done: {status: ok}}
`

const guardsWorkflow = `workflow: ship
start: wait_for_ci
guards:
  - id: merge-only-in-merge-step
    match: "gh pr merge .*"
    only_in: [merge_pr]
  - id: push-only-in-publish-step
    match: "git push .*"
    only_in: [publish]
steps:
  - id: wait_for_ci
    kind: wait
    poll: echo PENDING
    every: 60s
    timeout: 2h
    outcomes:
      GREEN: merge_pr
      timeout: blocked
  - id: merge_pr
    kind: deterministic
    run: echo merged
    postcondition: "true"
    next: publish
  - id: publish
    kind: deterministic
    run: echo published
    postcondition: "true"
    next: done
terminal:
  done: {status: ok, message: "Shipped."}
  blocked: {status: blocked, message: "CI did not finish."}
`

const invariantWorkflow = `workflow: ship
start: assign
state:
  reviewers_approved: {type: integer, default: 0}
invariants:
  - id: draft-until-assigned
    check: test ${reviewers_approved} -gt 0
    message: "The MR was un-drafted or given reviewers before any were approved."
steps:
  - id: assign
    kind: deterministic
    run: echo assigning
    postcondition: "true"
    next: done
terminal:
  done: {status: ok, message: "Assigned."}
`

const askWorkflow = `workflow: review
start: choose_reviewer
steps:
  - id: choose_reviewer
    kind: human
    question: "Who reviews this change?"
    options: [alice, bob, skip]
    timeout: 24h
    outcomes:
      alice: done
      bob: done
      skip: done
      timeout: blocked
terminal:
  done: {status: ok, message: "Reviewer chosen."}
  blocked: {status: blocked, message: "Nobody answered."}
`

const waitWorkflow = `workflow: ci
start: wait_for_ci
steps:
  - id: wait_for_ci
    kind: wait
    poll: echo GREEN
    every: 60s
    timeout: 2h
    outcomes:
      GREEN: done
      RED: blocked
      timeout: blocked
terminal:
  done: {status: ok, message: "CI is green."}
  blocked: {status: blocked, message: "CI failed."}
`

// greenTests is the dogfood project: testdata/fixture plus the green-tests
// example, laid out the way docs/dogfood.md says.
func greenTests(cmds ...docCmd) docScenario {
	return docScenario{
		gitRepo: true,
		copies: map[string]string{
			"go.mod":                                 "testdata/fixture/go.mod",
			"fixture.go":                             "testdata/fixture/fixture.go",
			"fixture_test.go":                        "testdata/fixture/fixture_test.go",
			".claude/workflows/green-tests.yaml":     "docs/examples/green-tests/workflow.yaml",
			".claude/workflows/scripts/run-tests.sh": "docs/examples/green-tests/scripts/run-tests.sh",
		},
		cmds: cmds,
	}
}

func greenTestsEnforced(cmds ...docCmd) docScenario {
	s := greenTests(cmds...)
	s.enforced = true
	return s
}

const fixSed = `sed -i 's/return a - b/return a + b/' fixture.go`

func submitFix() docCmd {
	return pawlCmd("submit", "--run", "{run}", "--step", "fix_tests", "--json", `{"fix_summary":"Fixed Add to use + instead of -"}`)
}

func hookPayload(event, command string) string {
	return fmt.Sprintf(`{"session_id":"s1","cwd":"{project}","hook_event_name":%q,"tool_name":"Bash","tool_input":{"command":%q}}`, event, command)
}

// docScenarios is keyed by the id in the <!-- pawl-capture: id --> marker.
var docScenarios = map[string]docScenario{
	"readme/tidy-run": {
		files: map[string]string{".claude/workflows/tidy.yaml": tidyWorkflow},
		// The workflow runs `ruff` and `pytest`; stubs keep the scenario
		// independent of what is installed. Their output is never shown.
		path:     map[string]string{"ruff": "exit 0", "pytest": "exit 0"},
		enforced: true,
		cmds:     []docCmd{pawlCmd("run", "tidy")},
	},

	"quickstart/validate-undeclared": {
		files: map[string]string{".claude/workflows/hello.yaml": strings.Replace(helloWorkflow, "args:\n  name: {type: string, required: true}\n", "", 1)},
		cmds:  []docCmd{exits(2, pawlCmd("validate", "hello"))},
	},
	"quickstart/validate": {
		files: map[string]string{".claude/workflows/hello.yaml": helloWorkflow},
		cmds:  []docCmd{pawlCmd("validate", "hello")},
	},
	"quickstart/validate-typo": {
		files: map[string]string{".claude/workflows/hello.yaml": strings.Replace(helloWorkflow, "next: count", "next: cuont", 1)},
		cmds:  []docCmd{exits(2, pawlCmd("validate", "hello"))},
	},
	"quickstart/run": {
		files:    map[string]string{".claude/workflows/hello.yaml": helloWorkflow},
		enforced: true,
		cmds:     []docCmd{pawlCmd("run", "hello", "name=ada")},
	},
	"quickstart/blocked": {
		files:    map[string]string{".claude/workflows/hello.yaml": helloBlocked},
		enforced: true,
		cmds:     []docCmd{exits(3, pawlCmd("run", "hello", "name=ada"))},
	},

	"validate/manage-mr": {
		copies: map[string]string{".claude/workflows/manage-mr.yaml": "docs/examples/manage-mr/workflow.yaml"},
		cmds:   []docCmd{pawlCmd("validate", "manage-mr")},
	},

	"cli/refuse-no-heartbeat": {
		files: map[string]string{".claude/workflows/hello.yaml": helloWorkflow},
		cmds:  []docCmd{{args: []string{"run", "hello", "name=ada"}, env: []string{"PAWL_ENFORCEMENT="}, exit: 4}},
	},
	"cli/enforcement-bound": {
		files:    map[string]string{".claude/workflows/hello.yaml": helloBlocked},
		enforced: true,
		cmds: []docCmd{
			hidden(exits(3, pawlCmd("run", "hello", "name=ada"))),
			exits(4, pawlCmd("run", "hello", "--no-enforcement")),
		},
	},
	// A one-step workflow, so the step named in the refusal is exactly the
	// step that was edited.
	"cli/workflow-changed": {
		files: map[string]string{".claude/workflows/hello.yaml": oneStepBlocked},
		cmds: []docCmd{
			hidden(exits(3, pawlCmd("run", "hello", "name=ada"))),
			hidden(docCmd{sh: "true", write: map[string]string{".claude/workflows/hello.yaml": strings.Replace(oneStepBlocked, "echo hello from", "echo hi from", 1)}}),
			exits(4, pawlCmd("run", "hello")),
		},
	},
	"cli/locked": {
		files: map[string]string{".claude/workflows/hello.yaml": helloBlocked},
		cmds: []docCmd{
			hidden(exits(3, pawlCmd("run", "hello", "name=ada"))),
			{args: []string{"run", "hello"}, locked: true, exit: 4},
		},
	},
	"troubleshooting/newer-format": {
		files: map[string]string{".claude/workflows/hello.yaml": helloBlocked},
		cmds: []docCmd{
			hidden(exits(3, pawlCmd("run", "hello", "name=ada"))),
			// Stamp the journal as written by a pawl one format ahead.
			hidden(docCmd{sh: `sed -i 's/"format":2/"format":3/' "$PAWL_STATE_DIR"/*/hello/*/events.jsonl`}),
			exits(4, pawlCmd("run", "hello")),
			pawlCmd("status"),
		},
	},
	"cli/finished-run": {
		files: map[string]string{".claude/workflows/hello.yaml": helloWorkflow},
		cmds: []docCmd{
			hidden(pawlCmd("run", "hello", "name=ada")),
			exits(4, pawlCmd("submit", "--run", "{run}", "--step", "greet", "--json", "{}")),
			exits(4, pawlCmd("abandon", "--run", "{run}")),
		},
	},
	"cli/status-json": {
		files: map[string]string{".claude/workflows/sample.yaml": sampleWorkflow},
		cmds: []docCmd{
			hidden(pawlCmd("run", "sample", "name=ada")),
			pawlCmd("status", "--json"),
		},
	},
	"cli/list": {
		copies: map[string]string{
			".claude/workflows/manage-mr.yaml":          "docs/examples/manage-mr/workflow.yaml",
			".claude/workflows/dependency-upgrade.yaml": "docs/examples/dependency-upgrade/workflow.yaml",
		},
		homeCopy: map[string]string{".claude/workflows/brain-dispatch.yaml": "docs/examples/brain-dispatch/workflow.yaml"},
		cmds:     []docCmd{pawlCmd("list")},
	},
	"cli/version": {
		cmds: []docCmd{pawlCmd("version")},
	},
	"cli/update-source-build": {
		cmds: []docCmd{exits(4, pawlCmd("update"))},
	},

	"troubleshooting/submit-wrong-step": {
		files: map[string]string{".claude/workflows/sample.yaml": sampleWorkflow},
		cmds: []docCmd{
			hidden(pawlCmd("run", "sample", "name=ada")),
			exits(4, pawlCmd("submit", "--run", "{run}", "--step", "farewell", "--json", `{"greeting":"hi"}`)),
		},
	},
	"troubleshooting/workflow-not-found": {
		files:  map[string]string{"sub/.keep": ""},
		copies: map[string]string{".claude/workflows/manage-mr.yaml": "docs/examples/manage-mr/workflow.yaml"},
		noVCS:  true,
		cmds:   []docCmd{{args: []string{"run", "manage-mr"}, dir: "sub", exit: 1}},
	},
	"troubleshooting/stop-blocked": {
		files:    map[string]string{".claude/workflows/sample.yaml": sampleWorkflow},
		enforced: true,
		cmds: []docCmd{
			hidden(pawlCmd("run", "sample", "name=ada")),
			{args: []string{"hook", "stop"}, stdin: hookPayload("Stop", ""), show: `echo '<Stop hook payload>' | pawl hook stop`},
		},
	},

	"guards/banner": {
		files:    map[string]string{".claude/workflows/ship.yaml": guardsWorkflow},
		enforced: true,
		cmds:     []docCmd{pawlCmd("run", "ship")},
	},
	"guards/deny": {
		files:    map[string]string{".claude/workflows/ship.yaml": guardsWorkflow},
		enforced: true,
		cmds: []docCmd{
			hidden(pawlCmd("run", "ship")),
			{args: []string{"hook", "pre"}, stdin: hookPayload("PreToolUse", "gh pr merge 41"), show: `echo '<PreToolUse payload for: gh pr merge 41>' | pawl hook pre`},
		},
	},
	"guards/invariant-blocked": {
		files:    map[string]string{".claude/workflows/ship.yaml": invariantWorkflow},
		enforced: true,
		cmds:     []docCmd{exits(3, pawlCmd("run", "ship"))},
	},

	"running/run": greenTestsEnforced(pawlCmd("run", "green-tests")),
	"running/status": greenTestsEnforced(
		hidden(pawlCmd("run", "green-tests")),
		pawlCmd("status"),
	),
	"running/resume": greenTestsEnforced(
		hidden(pawlCmd("run", "green-tests")),
		pawlCmd("run", "green-tests"),
	),
	"running/ask": {
		files:    map[string]string{".claude/workflows/review.yaml": askWorkflow},
		enforced: true,
		cmds:     []docCmd{pawlCmd("run", "review")},
	},
	"running/wait": {
		files:    map[string]string{".claude/workflows/ci.yaml": waitWorkflow},
		enforced: true,
		cmds: []docCmd{
			pawlCmd("run", "ci"),
			pawlCmd("poll", "--run", "{run}", "--step", "wait_for_ci"),
		},
	},

	"dogfood/run": greenTests(pawlCmd("run", "green-tests", "--no-enforcement")),
	"dogfood/submit": greenTests(
		hidden(pawlCmd("run", "green-tests", "--no-enforcement")),
		hidden(docCmd{sh: fixSed}),
		submitFix(),
	),
	"dogfood/status": greenTests(
		hidden(pawlCmd("run", "green-tests", "--no-enforcement")),
		hidden(docCmd{sh: fixSed}),
		hidden(submitFix()),
		pawlCmd("status"),
	),
}

// runIDContext matches the places pawl prints a run id; the id itself is
// spliced in at %s.
const runIDContext = `(\b(?:DISPATCH|DISPATCH_PARALLEL|WAIT|ASK|TERMINAL|run|run:|--run) "?|"run_id":")%s\b`

// normalize is the ONLY place a transcript is rewritten before it is compared
// with (or written into) a doc. The rules, in order:
//
//  1. The scenario's project directory (a fresh temp dir, symlinks resolved)
//     becomes /home/you/project, and its HOME directory becomes /home/you.
//  2. Run ids, which pawl draws at random, become 7f3a, 4b17, 6d02, ... in
//     order of first appearance in the transcript. An id is only rewritten
//     where pawl prints one: after DISPATCH, DISPATCH_PARALLEL, WAIT, ASK,
//     TERMINAL, "run", "run:" or "--run" (each followed by one space and an
//     optional double quote), or as
//     the "run_id" JSON value.
//  3. Elapsed times printed by `go test` ("0.002s", "(0.00s)") become 0.00s,
//     and `pawl poll`'s "(2s parked)" becomes "(0s parked)".
//  4. The pid in a "locked by pid N" refusal (the harness holds the lock with
//     a throwaway `sleep` process) becomes 48122.
//
// There is no version rule: the test binary is a source build, which prints
// "pawl dev" every time. There are no timestamps in any captured output.
// Nothing else is touched.
func normalize(text, project, home string, runIDs []string) string {
	text = strings.ReplaceAll(text, project, "/home/you/project")
	text = strings.ReplaceAll(text, home, "/home/you")
	fixed := []string{"7f3a", "4b17", "6d02", "9c8c", "a98d"}
	type seen struct {
		re  *regexp.Regexp
		pos int
	}
	var order []seen
	for _, id := range runIDs {
		re := regexp.MustCompile(fmt.Sprintf(runIDContext, regexp.QuoteMeta(id)))
		if loc := re.FindStringIndex(text); loc != nil {
			order = append(order, seen{re, loc[0]})
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].pos < order[j].pos })
	// Via placeholders, so a replacement can never be re-matched as another
	// run's id.
	for i, s := range order {
		text = s.re.ReplaceAllString(text, fmt.Sprintf("${1}@@RUN%d@@", i))
	}
	for i := range order {
		text = strings.ReplaceAll(text, fmt.Sprintf("@@RUN%d@@", i), fixed[i])
	}
	text = regexp.MustCompile(`\b\d+\.\d+s\b`).ReplaceAllString(text, "0.00s")
	text = regexp.MustCompile(`\((?:\d+[hms])+ parked\)`).ReplaceAllString(text, "(0s parked)")
	return regexp.MustCompile(`locked by pid \d+`).ReplaceAllString(text, "locked by pid 48122")
}

var runIDDirName = regexp.MustCompile(`^[0-9a-f]{4}$`)

// runDocScenario executes s and returns its normalised transcript.
func runDocScenario(t *testing.T, s docScenario) string {
	t.Helper()
	repo := repoRoot(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	stateDir := filepath.Join(base, "state")
	binDir := filepath.Join(base, "bin")
	for _, d := range []string{project, home, stateDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A bare .git directory is all pawl's root resolution needs.
	if !s.noVCS && !s.gitRepo {
		if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put := func(root, rel string, data []byte) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	read := func(src string) []byte {
		data, err := os.ReadFile(filepath.Join(repo, src))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for rel, c := range s.files {
		put(project, rel, []byte(c))
	}
	for rel, src := range s.copies {
		put(project, rel, read(src))
	}
	for rel, src := range s.homeCopy {
		put(home, rel, read(src))
	}
	for name, body := range s.path {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if s.gitRepo {
		// Scenarios whose workflow has a `!git …` context entry run in a real
		// repository with the files committed, so the output is what a user
		// who committed them sees. Config comes from flags and env only.
		gitEnv := append(os.Environ(), "HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.com", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.com")
		for _, args := range [][]string{
			{"-c", "init.defaultBranch=main", "init", "-q"},
			{"add", "-A"},
			{"-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = project
			cmd.Env = gitEnv
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}

	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PAWL_") || strings.HasPrefix(kv, "CLAUDE_CODE_SESSION_ID=") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "PATH=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+home, "PAWL_STATE_DIR="+stateDir)
	if s.enforced {
		env = append(env, "CLAUDE_CODE_SESSION_ID=s1")
	} else {
		env = append(env, "PAWL_ENFORCEMENT=off")
	}

	runIDs := map[string]bool{}
	// Runs live at <state>/<working-copy slug>/<workflow>/<run id>.
	collectRunIDs := func() {
		dirs, _ := filepath.Glob(filepath.Join(stateDir, "*", "*", "*"))
		for _, d := range dirs {
			if runIDDirName.MatchString(filepath.Base(d)) {
				runIDs[filepath.Base(d)] = true
			}
		}
	}
	execIn := func(name string, args []string, stdin, dir string, extra []string) (string, int) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(append([]string{}, env...), extra...)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("running %s %v: %v", name, args, err)
			}
			code = ee.ExitCode()
		}
		return buf.String(), code
	}

	if s.enforced {
		// The plugin's PreToolUse hook fires before the session's first
		// `pawl run` Bash call and records the heartbeat that call needs.
		payload := strings.ReplaceAll(hookPayload("PreToolUse", "pawl run"), "{project}", project)
		if out, code := execIn(wfBinary(t), []string{"hook", "pre"}, payload, project, nil); code != 0 {
			t.Fatalf("pawl hook pre: exit %d\n%s", code, out)
		}
	}

	var transcript strings.Builder
	var firstRun string
	for _, c := range s.cmds {
		for rel, content := range c.write {
			put(project, rel, []byte(content))
		}
		dir := filepath.Join(project, c.dir)
		release := func() {}
		if c.locked {
			release = holdRunLock(t, stateDir)
		}
		var out string
		var code int
		var line string
		if c.sh != "" {
			out, code = execIn("sh", []string{"-c", c.sh}, "", dir, c.env)
			line = c.sh
		} else {
			args := make([]string, len(c.args))
			for i, a := range c.args {
				args[i] = strings.ReplaceAll(a, "{run}", firstRun)
			}
			stdin := strings.ReplaceAll(c.stdin, "{project}", project)
			out, code = execIn(wfBinary(t), args, stdin, dir, c.env)
			line = "pawl " + shellJoin(args)
		}
		release()
		if code != c.exit {
			t.Fatalf("%s: exit %d, want %d\n%s", line, code, c.exit, out)
		}
		collectRunIDs()
		if firstRun == "" {
			for id := range runIDs {
				firstRun = id
			}
		}
		if c.hidden {
			continue
		}
		if c.show != "" {
			line = c.show
		}
		transcript.WriteString("$ " + line + "\n")
		transcript.WriteString(out)
		if out != "" && !strings.HasSuffix(out, "\n") {
			transcript.WriteString("\n")
		}
		if code != 0 {
			fmt.Fprintf(&transcript, "exit %d\n", code)
		}
	}
	var ids []string
	for id := range runIDs {
		ids = append(ids, id)
	}
	return strings.TrimRight(normalize(transcript.String(), project, home, ids), "\n")
}

func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`<>|&;(){}*?[]#~!") {
			parts[i] = a
		} else {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

// docBlock is one captured block found in a markdown file: lines[start:end]
// are the content between the fences.
type docBlock struct {
	id         string
	start, end int
}

var captureMarker = regexp.MustCompile(`^<!-- pawl-capture: (\S+) -->$`)

func findDocBlocks(t *testing.T, file string, lines []string) []docBlock {
	t.Helper()
	var blocks []docBlock
	for i, l := range lines {
		m := captureMarker.FindStringSubmatch(l)
		if m == nil {
			if strings.Contains(l, "pawl-capture:") && strings.HasPrefix(l, "<!--") {
				t.Errorf("%s:%d: malformed pawl-capture marker %q", file, i+1, l)
			}
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "```") {
			t.Errorf("%s:%d: pawl-capture %s must be followed immediately by a fenced block", file, i+1, m[1])
			continue
		}
		end := -1
		for j := i + 2; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "```") {
				end = j
				break
			}
		}
		if end < 0 {
			t.Errorf("%s:%d: pawl-capture %s: unterminated fenced block", file, i+1, m[1])
			continue
		}
		blocks = append(blocks, docBlock{id: m[1], start: i + 2, end: end})
	}
	return blocks
}

func TestDocsOutput(t *testing.T) {
	root := repoRoot(t)
	docsDir := filepath.Join(root, "docs")
	update := os.Getenv("UPDATE_DOCS") == "1"
	skip := map[string]bool{"examples": true, "review": true, "superpowers": true}

	var files []string
	err := filepath.WalkDir(docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skip[d.Name()] && filepath.Dir(p) == docsDir {
			return fs.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	transcripts := map[string]string{}
	_, gitErr := exec.LookPath("git")
	skipped := map[string]bool{}
	transcript := func(id string) (string, bool) {
		if s, ok := transcripts[id]; ok {
			return s, true
		}
		sc, ok := docScenarios[id]
		if !ok {
			return "", false
		}
		if sc.gitRepo && gitErr != nil {
			if os.Getenv("PAWL_REQUIRE_VCS_TOOLS") == "1" {
				t.Fatalf("scenario %q needs git: %v", id, gitErr)
			}
			skipped[id] = true
			return "", false
		}
		transcripts[id] = runDocScenario(t, sc)
		return transcripts[id], true
	}

	used := map[string]bool{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		rel, _ := filepath.Rel(root, file)
		blocks := findDocBlocks(t, rel, lines)
		changed := false
		// Rewrite bottom-up so earlier line numbers stay valid.
		for i := len(blocks) - 1; i >= 0; i-- {
			b := blocks[i]
			used[b.id] = true
			want, ok := transcript(b.id)
			if !ok && skipped[b.id] {
				t.Logf("%s:%d: skipping %q (git not on PATH)", rel, b.start, b.id)
				continue
			}
			if !ok {
				t.Errorf("%s:%d: no scenario %q in docScenarios", rel, b.start, b.id)
				continue
			}
			got := strings.Join(lines[b.start:b.end], "\n")
			if got == want {
				continue
			}
			if update {
				lines = append(lines[:b.start], append(strings.Split(want, "\n"), lines[b.end:]...)...)
				changed = true
				continue
			}
			t.Errorf("%s:%d: block for %q is stale (rerun with UPDATE_DOCS=1 to rewrite it)\n--- doc ---\n%s\n--- real ---\n%s", rel, b.start, b.id, got, want)
		}
		if changed {
			if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for id := range docScenarios {
		if !used[id] {
			t.Errorf("scenario %q has no <!-- pawl-capture: %s --> marker in docs/", id, id)
		}
	}
}

// holdRunLock makes the (single) run under stateDir look locked by a live
// process: it starts a throwaway `sleep` and records its pid in the run's
// lock file, exactly where pawl's own lock lives. The returned func kills the
// process and removes the lock.
func holdRunLock(t *testing.T, stateDir string) func() {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(stateDir, "*", "*", "*"))
	var runDir string
	for _, d := range dirs {
		if runIDDirName.MatchString(filepath.Base(d)) {
			runDir = d
		}
	}
	if runDir == "" {
		t.Fatal("holdRunLock: no run directory yet")
	}
	holder := exec.Command("sleep", "60")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(runDir, "lock")
	if err := os.WriteFile(lock, []byte(fmt.Sprint(holder.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
		_ = os.Remove(lock)
	}
}
