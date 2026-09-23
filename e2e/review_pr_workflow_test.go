package e2e

// Coverage for this repo's own .claude/workflows/review-pr.yaml: the
// workflow file must keep validating as spec/validator rules evolve, and
// its shell scripts — three of which push to a PR branch — are exercised
// against temporary git (and, when installed, jj) repos with a local bare
// "origin" and a fake `gh` on PATH, so no network or GitHub account is
// involved.
//
// Tool gating follows internal/journal/root_test.go: a missing git or jq
// skips locally but fails when PAWL_REQUIRE_VCS_TOOLS is set (this repo's
// CI installs both); a missing jj always skips, with an explicit message.

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

const requireVCSToolsEnv = "PAWL_REQUIRE_VCS_TOOLS"

func reviewPRDir(t *testing.T) string {
	return filepath.Join(repoRoot(t), ".claude", "workflows")
}

func reviewPRScript(t *testing.T, name string) string {
	return filepath.Join(reviewPRDir(t), "scripts", name)
}

func TestReviewPRWorkflowValidates(t *testing.T) {
	path := filepath.Join(reviewPRDir(t), "review-pr.yaml")
	w, err := spec.Load(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	report, err := spec.Validate(w)
	if err != nil {
		t.Fatalf("validating %s: %v", path, err)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("%s has validation errors:\n%s", path, strings.Join(report.Errors, "\n"))
	}
}

// needTool skips (or, with hard set and PAWL_REQUIRE_VCS_TOOLS set, fails)
// when name isn't on PATH.
func needTool(t *testing.T, name string, hard bool) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if hard && os.Getenv(requireVCSToolsEnv) != "" {
			t.Fatalf("%s is required when %s is set: %v", name, requireVCSToolsEnv, err)
		}
		t.Skipf("SKIPPING: %s is not installed (%v) — review-pr script coverage that needs it is reduced here", name, err)
	}
}

// fakeGH is a stand-in `gh` covering exactly the subcommands the review-pr
// scripts call. `pr view` answers from $FAKE_GH_VIEW (a JSON file) when set,
// otherwise derives headRefOid from the bare remote's $FAKE_GH_BRANCH;
// `pr diff` diffs that branch against main in the bare remote; `api` for a
// git ref reads the ref from the bare remote; `run view` prints
// $FAKE_GH_RUNLOG. $FAKE_GH_FAIL makes every call fail; $FAKE_GH_FAIL_DIFF
// makes only `pr diff` fail (after writing partial output).
const fakeGH = `#!/bin/sh
if [ -n "${FAKE_GH_FAIL:-}" ]; then echo "gh: simulated failure" >&2; exit 1; fi
if [ -n "${FAKE_GH_FAIL_DIFF:-}" ] && [ "$1 $2" = "pr diff" ]; then
  echo "diff --git a/partial b/partial"; echo "gh: simulated diff failure" >&2; exit 1
fi
case "$1 $2" in
"pr view")
  if [ -n "${FAKE_GH_VIEW:-}" ]; then cat "$FAKE_GH_VIEW"; exit 0; fi
  sha=$(git --git-dir="$FAKE_GH_REMOTE" rev-parse "refs/heads/$FAKE_GH_BRANCH") || exit 1
  printf '{"headRefOid":"%s"}\n' "$sha" ;;
"pr diff")
  git --git-dir="$FAKE_GH_REMOTE" diff "refs/heads/main...refs/heads/$FAKE_GH_BRANCH" ;;
"run view")
  cat "$FAKE_GH_RUNLOG" ;;
"api "*)
  branch=${2#repos/\{owner\}/\{repo\}/git/ref/heads/}
  git --git-dir="$FAKE_GH_REMOTE" rev-parse "refs/heads/$branch" ;;
*)
  echo "fake gh: unhandled: $*" >&2; exit 1 ;;
esac
`

type scriptEnv struct {
	dir string   // working directory scripts run in
	env []string // full environment
}

// newScriptEnv writes the fake gh into a temp bin dir and returns an
// environment isolated from the user's git/jj config and cache dir.
func newScriptEnv(t *testing.T) *scriptEnv {
	t.Helper()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	jjConfig := filepath.Join(tmp, "jjconfig.toml")
	if err := os.WriteFile(jjConfig, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + tmp,
		"XDG_CACHE_HOME=" + filepath.Join(tmp, "cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(tmp, "config"),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"JJ_CONFIG=" + jjConfig,
		"JJ_USER=t", "JJ_EMAIL=t@example.com",
		"PAWL_REVIEW_HEAD_TRIES=1",
		"PAWL_REVIEW_HEAD_SLEEP=0",
	}
	return &scriptEnv{dir: tmp, env: env}
}

func (e *scriptEnv) set(kv ...string) { e.env = append(e.env, kv...) }

// run runs name with args in e.dir and returns stdout, stderr and the exit
// code (a failure to start at all fails the test).
func (e *scriptEnv) run(t *testing.T, name string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = e.dir
	cmd.Env = e.env
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out.String(), errb.String(), 0
	case errors.As(err, &exitErr):
		return out.String(), errb.String(), exitErr.ExitCode()
	default:
		t.Fatalf("running %s: %v", name, err)
		return "", "", -1
	}
}

// must runs name and fails the test on a non-zero exit, returning trimmed
// stdout.
func (e *scriptEnv) must(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, errOut, code := e.run(t, name, args...)
	if code != 0 {
		t.Fatalf("%s %v: exit %d\nstdout: %s\nstderr: %s", name, args, code, out, errOut)
	}
	return strings.TrimSpace(out)
}

// lastLine is the line the engine parses (design/format-spec.md §B.1).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestReviewPRScript_ReviewRoute(t *testing.T) {
	needTool(t, "jq", true)
	e := newScriptEnv(t)
	script := reviewPRScript(t, "review-route.sh")

	out := e.must(t, script, "[]", "[]", "[]", "[]")
	if lastLine(out) != `clean {"findings": []}` {
		t.Errorf("all empty: got %q", out)
	}

	out = e.must(t, script, `[{"file":"a","line":1}]`, "[]", "[]", `[{"file":"b","line":2}]`)
	tok, payload, _ := strings.Cut(lastLine(out), " ")
	if tok != "blocking" {
		t.Fatalf("non-empty: token %q, want blocking (%q)", tok, out)
	}
	var got struct{ Findings []map[string]any }
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload %q: %v", payload, err)
	}
	if len(got.Findings) != 2 {
		t.Errorf("merged %d findings, want 2: %q", len(got.Findings), payload)
	}
}

func TestReviewPRScript_PollCI(t *testing.T) {
	needTool(t, "jq", true)
	const sha = "abc123"
	checkRun := func(status, conclusion string) string {
		return `{"__typename":"CheckRun","name":"test","status":"` + status + `","conclusion":"` + conclusion + `"}`
	}
	view := func(head string, rollup ...string) string {
		return `{"headRefOid":"` + head + `","statusCheckRollup":[` + strings.Join(rollup, ",") + `]}`
	}
	cases := []struct {
		name  string
		view  string // "" = gh fails
		allow string
		want  string
	}{
		{"gh error", "", "false", "PENDING"},
		{"no checks yet", view(sha), "false", "PENDING"},
		{"no checks, repo without CI", view(sha), "true", "SUCCESS"},
		{"head not updated yet", view("oldsha", checkRun("COMPLETED", "SUCCESS")), "false", "PENDING"},
		{"pending", view(sha, checkRun("COMPLETED", "SUCCESS"), checkRun("IN_PROGRESS", "")), "false", "PENDING"},
		{"fail", view(sha, checkRun("COMPLETED", "SUCCESS"), checkRun("COMPLETED", "FAILURE")), "false", "FAILURE"},
		{"cancel", view(sha, checkRun("COMPLETED", "CANCELLED")), "false", "FAILURE"},
		{"fail beats pending", view(sha, checkRun("QUEUED", ""), checkRun("COMPLETED", "TIMED_OUT")), "false", "FAILURE"},
		{"pass and skipped", view(sha, checkRun("COMPLETED", "SUCCESS"), checkRun("COMPLETED", "SKIPPED")), "false", "SUCCESS"},
		{"status context error", view(sha, `{"__typename":"StatusContext","context":"ci/x","state":"ERROR"}`), "false", "FAILURE"},
		{"status context pending", view(sha, `{"__typename":"StatusContext","context":"ci/x","state":"PENDING"}`), "false", "PENDING"},
		{"garbage output", "not json", "false", "PENDING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newScriptEnv(t)
			if tc.view == "" {
				e.set("FAKE_GH_FAIL=1")
			} else {
				f := filepath.Join(e.dir, "view.json")
				if err := os.WriteFile(f, []byte(tc.view), 0o644); err != nil {
					t.Fatal(err)
				}
				e.set("FAKE_GH_VIEW=" + f)
			}
			out, errOut, code := e.run(t, reviewPRScript(t, "poll-ci.sh"), "7", sha, tc.allow)
			if code != 0 {
				t.Fatalf("poll-ci.sh must always exit 0, got %d (stderr %s)", code, errOut)
			}
			if got := lastLine(out); got != tc.want {
				t.Errorf("got %q, want %q (stderr %s)", got, tc.want, errOut)
			}
		})
	}
}

func TestReviewPRScript_CIFailures(t *testing.T) {
	needTool(t, "jq", true)
	e := newScriptEnv(t)
	view := filepath.Join(e.dir, "view.json")
	if err := os.WriteFile(view, []byte(`{"statusCheckRollup":[
	  {"__typename":"CheckRun","name":"lint","workflowName":"ci","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":"https://github.com/o/r/actions/runs/1/job/10"},
	  {"__typename":"CheckRun","name":"test","workflowName":"ci","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://github.com/o/r/actions/runs/1/job/11"},
	  {"__typename":"StatusContext","context":"ext/ci","state":"FAILURE","targetUrl":"https://ci.example/1"}
	]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runlog := filepath.Join(e.dir, "run.log")
	if err := os.WriteFile(runlog, []byte("test\tstep\tFAIL: TestAdd (0.00s)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.set("FAKE_GH_VIEW="+view, "FAKE_GH_RUNLOG="+runlog)

	out := e.must(t, reviewPRScript(t, "ci-failures.sh"), "7", "abc123")
	var got struct {
		Findings []struct {
			File        string `json:"file"`
			Line        int    `json:"line"`
			Category    string `json:"category"`
			Description string `json:"description"`
			Fix         string `json:"fix"`
		}
	}
	if err := json.Unmarshal([]byte(lastLine(out)), &got); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if len(got.Findings) != 2 {
		t.Fatalf("got %d findings, want 2 (test + ext/ci): %q", len(got.Findings), out)
	}
	if got.Findings[0].File != "CI: ci / test" || !strings.Contains(got.Findings[0].Description, "FAIL: TestAdd") {
		t.Errorf("first finding should name the failed job and carry its log: %+v", got.Findings[0])
	}
	if got.Findings[1].File != "CI: ext/ci" || !strings.Contains(got.Findings[1].Description, "https://ci.example/1") {
		t.Errorf("second finding should name the status context and link it: %+v", got.Findings[1])
	}

	// Nothing failing any more: still one finding, never an empty fix round.
	if err := os.WriteFile(view, []byte(`{"statusCheckRollup":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out = e.must(t, reviewPRScript(t, "ci-failures.sh"), "7", "abc123")
	if err := json.Unmarshal([]byte(lastLine(out)), &got); err != nil || len(got.Findings) != 1 {
		t.Errorf("no failing checks listed: want exactly one fallback finding, got %q (%v)", out, err)
	}
}

// setupRemote creates a bare "origin" with main (one file) and feature (one
// more commit on top), and points the fake gh at it for a PR from feature.
func setupRemote(t *testing.T, e *scriptEnv) string {
	t.Helper()
	remote := filepath.Join(e.dir, "origin.git")
	seed := filepath.Join(e.dir, "seed")
	e.must(t, "git", "init", "--quiet", "--bare", remote)
	e.must(t, "git", "init", "--quiet", seed)
	gitIn := func(args ...string) string { return e.must(t, "git", append([]string{"-C", seed}, args...)...) }
	write(t, filepath.Join(seed, "a.txt"), "one\n")
	gitIn("add", "-A")
	gitIn("commit", "--quiet", "-m", "base")
	gitIn("push", "--quiet", remote, "HEAD:refs/heads/main")
	write(t, filepath.Join(seed, "a.txt"), "one\ntwo\n")
	gitIn("commit", "--quiet", "-am", "feature")
	gitIn("push", "--quiet", remote, "HEAD:refs/heads/feature")
	e.set("FAKE_GH_REMOTE="+remote, "FAKE_GH_BRANCH=feature")
	return remote
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func remoteHead(t *testing.T, e *scriptEnv, remote string) string {
	t.Helper()
	return e.must(t, "git", "--git-dir="+remote, "rev-parse", "refs/heads/feature")
}

// checkPrepare runs prepare-review.sh and checks it wrote the remote head
// and a non-empty diff file outside the working copy.
func checkPrepare(t *testing.T, e *scriptEnv, vcs, remote string) {
	t.Helper()
	out := e.must(t, reviewPRScript(t, "prepare-review.sh"), vcs, "7")
	var got struct {
		HeadSHA  string `json:"head_sha"`
		DiffFile string `json:"diff_file"`
	}
	if err := json.Unmarshal([]byte(lastLine(out)), &got); err != nil {
		t.Fatalf("prepare-review.sh output %q: %v", out, err)
	}
	if want := remoteHead(t, e, remote); got.HeadSHA != want {
		t.Errorf("head_sha %q, want %q", got.HeadSHA, want)
	}
	if strings.HasPrefix(got.DiffFile, e.dir+string(filepath.Separator)+"wc") {
		t.Errorf("diff file %q is inside the working copy", got.DiffFile)
	}
	data, err := os.ReadFile(got.DiffFile)
	if err != nil || !strings.Contains(string(data), "+two") {
		t.Errorf("diff file %q should hold the PR diff: %q (%v)", got.DiffFile, data, err)
	}
}

// exercisePushFlow is the VCS-independent part of the git and jj tests:
// e.dir is a clean working copy sitting on the PR head.
func exercisePushFlow(t *testing.T, e *scriptEnv, vcs, remote string) {
	t.Helper()
	prepare := reviewPRScript(t, "prepare-review.sh")
	fixPush := reviewPRScript(t, "fix-push.sh")
	verify := reviewPRScript(t, "verify-pushed.sh")

	// The diff is a hard input: a gh failure fails the round's first step
	// instead of letting the reviewers see an empty diff — and is reported
	// as a gh failure, not as a misleading head mismatch.
	failing := &scriptEnv{dir: e.dir, env: append(append([]string{}, e.env...), "FAKE_GH_FAIL=1")}
	if _, errOut, code := failing.run(t, prepare, vcs, "7"); code == 0 {
		t.Error("prepare-review.sh succeeded although gh failed")
	} else if !strings.Contains(errOut, "gh pr view 7 failed") {
		t.Errorf("gh pr view failure misreported: %s", errOut)
	}
	// Only `gh pr diff` failing (pr view works): the diff-failure branch
	// fires and leaves no partial diff file behind.
	diffFailing := &scriptEnv{dir: e.dir, env: append(append([]string{}, e.env...), "FAKE_GH_FAIL_DIFF=1")}
	if _, errOut, code := diffFailing.run(t, prepare, vcs, "7"); code == 0 {
		t.Error("prepare-review.sh succeeded although gh pr diff failed")
	} else if !strings.Contains(errOut, "gh pr diff 7 failed") {
		t.Errorf("gh pr diff failure misreported: %s", errOut)
	}
	cacheDir := filepath.Join(e.dir, "..", "cache", "pawl-review-pr")
	if entries, err := os.ReadDir(cacheDir); err == nil {
		for _, ent := range entries {
			t.Errorf("gh pr diff failure left %s behind in the cache dir", ent.Name())
		}
	}

	checkPrepare(t, e, vcs, remote)
	e.must(t, verify, vcs, "feature")

	// No changes: nothing committed or pushed, distinct token.
	before := remoteHead(t, e, remote)
	if out := e.must(t, fixPush, vcs, "feature", "0"); lastLine(out) != "unchanged" {
		t.Errorf("no changes: got %q, want unchanged", out)
	}
	if after := remoteHead(t, e, remote); after != before {
		t.Errorf("no changes: remote moved %s -> %s", before, after)
	}
	e.must(t, verify, vcs, "feature")

	// A dirty working copy refuses to start a round.
	write(t, filepath.Join(e.dir, "a.txt"), "one\ntwo\nthree\n")
	if _, _, code := e.run(t, prepare, vcs, "7"); code == 0 {
		t.Error("prepare-review.sh accepted a dirty working copy")
	}

	// Changes: committed as round 1, fast-forward pushed, verified.
	if out := e.must(t, fixPush, vcs, "feature", "0"); lastLine(out) != "pushed round=1" {
		t.Errorf("changes: got %q, want pushed round=1", out)
	}
	if after := remoteHead(t, e, remote); after == before {
		t.Error("changes: remote branch did not move")
	}
	e.must(t, verify, vcs, "feature")
	checkPrepare(t, e, vcs, remote)

	// Idempotent across a failed push: a previous attempt committed the fix
	// but the push never landed. The re-run must push that commit rather
	// than report `unchanged` (which verify-pushed.sh would then reject).
	write(t, filepath.Join(e.dir, "a.txt"), "one\ntwo\nthree\nfive\n")
	switch vcs {
	case "git":
		e.must(t, "git", "commit", "--quiet", "-am", "review round 2")
	case "jj":
		e.must(t, "jj", "commit", "-m", "review round 2")
	}
	if _, _, code := e.run(t, verify, vcs, "feature"); code == 0 {
		t.Fatal("setup: verify-pushed.sh passed before the stranded commit was pushed")
	}
	if out := e.must(t, fixPush, vcs, "feature", "1"); lastLine(out) != "pushed round=2" {
		t.Errorf("stranded commit: got %q, want pushed round=2", out)
	}
	e.must(t, verify, vcs, "feature")
	checkPrepare(t, e, vcs, remote)

	// Someone else moves the PR branch: local is no longer the PR head.
	other := filepath.Join(filepath.Dir(remote), "other")
	e.must(t, "git", "clone", "--quiet", "--branch", "feature", remote, other)
	write(t, filepath.Join(other, "b.txt"), "theirs\n")
	e.must(t, "git", "-C", other, "add", "-A")
	e.must(t, "git", "-C", other, "commit", "--quiet", "-m", "theirs")
	e.must(t, "git", "-C", other, "push", "--quiet", "origin", "HEAD:refs/heads/feature")
	theirs := remoteHead(t, e, remote)

	if _, _, code := e.run(t, prepare, vcs, "7"); code == 0 {
		t.Error("prepare-review.sh accepted a local head that is not the PR head")
	}
	if _, _, code := e.run(t, verify, vcs, "feature"); code == 0 {
		t.Error("verify-pushed.sh passed with the remote on someone else's commit")
	}
	// And a fix on the stale head must not overwrite their commit — refused
	// by fix-push.sh's own fast-forward guard (after fetching), before it
	// commits anything locally, not merely by a push-time lease check.
	headBefore := localHead(t, e, vcs)
	write(t, filepath.Join(e.dir, "a.txt"), "one\ntwo\nfour\n")
	if _, errOut, code := e.run(t, fixPush, vcs, "feature", "2"); code == 0 {
		t.Error("fix-push.sh pushed a change that does not descend from the remote branch")
	} else if !strings.Contains(errOut, "refusing to push") {
		t.Errorf("fix-push.sh failed, but not via its own fast-forward guard: %s", errOut)
	}
	if got := remoteHead(t, e, remote); got != theirs {
		t.Errorf("remote branch rewritten: %s, want %s", got, theirs)
	}
	if got := localHead(t, e, vcs); got != headBefore {
		t.Errorf("refused fix was still committed locally: head %s, want %s", got, headBefore)
	}
}

// localHead is the workflow's "local head": HEAD under git, @- under jj.
func localHead(t *testing.T, e *scriptEnv, vcs string) string {
	t.Helper()
	if vcs == "jj" {
		return e.must(t, "jj", "log", "--no-graph", "-r", "@-", "-T", "commit_id")
	}
	return e.must(t, "git", "rev-parse", "HEAD")
}

func TestReviewPRScripts_GitPushFlow(t *testing.T) {
	needTool(t, "git", true)
	needTool(t, "jq", true)
	e := newScriptEnv(t)
	remote := setupRemote(t, e)
	wc := filepath.Join(e.dir, "wc")
	e.must(t, "git", "clone", "--quiet", "--branch", "feature", remote, wc)
	e.dir = wc
	if got := e.must(t, reviewPRScript(t, "detect-vcs.sh")); got != "git" {
		t.Fatalf("detect-vcs.sh: %q, want git", got)
	}
	exercisePushFlow(t, e, "git", remote)
}

// An empty PR diff (branch == base) fails prepare-review.sh: a round must
// never "review" nothing and conclude the PR is clean.
func TestReviewPRScripts_EmptyDiffFails(t *testing.T) {
	needTool(t, "git", true)
	needTool(t, "jq", true)
	e := newScriptEnv(t)
	remote := setupRemote(t, e)
	main := e.must(t, "git", "--git-dir="+remote, "rev-parse", "refs/heads/main")
	e.must(t, "git", "--git-dir="+remote, "update-ref", "refs/heads/feature", main)
	wc := filepath.Join(e.dir, "wc")
	e.must(t, "git", "clone", "--quiet", "--branch", "feature", remote, wc)
	e.dir = wc
	if out, errOut, code := e.run(t, reviewPRScript(t, "prepare-review.sh"), "git", "7"); code == 0 {
		t.Errorf("prepare-review.sh accepted an empty diff: %q", out)
	} else if !strings.Contains(errOut, "empty diff") {
		t.Errorf("unexpected failure reason: %s", errOut)
	}
}

func TestReviewPRScripts_JJPushFlow(t *testing.T) {
	needTool(t, "git", true)
	needTool(t, "jq", true)
	needTool(t, "jj", false)
	e := newScriptEnv(t)
	remote := setupRemote(t, e)
	wc := filepath.Join(e.dir, "wc")
	e.must(t, "jj", "git", "clone", "--quiet", remote, wc)
	e.dir = wc
	e.must(t, "jj", "bookmark", "track", "feature", "--remote=origin")
	e.must(t, "jj", "new", "feature")
	if got := e.must(t, reviewPRScript(t, "detect-vcs.sh")); got != "jj" {
		t.Fatalf("detect-vcs.sh: %q, want jj", got)
	}
	exercisePushFlow(t, e, "jj", remote)
}

func TestReviewPRScript_FetchPR(t *testing.T) {
	needTool(t, "jq", true)
	view := func(cross, head, base string) string {
		return `{"url":"https://github.com/o/r/pull/7","headRefOid":"abc123","baseRefName":"` + base +
			`","headRefName":"` + head + `","title":"a title with spaces","isCrossRepository":` + cross + `}`
	}
	cases := []struct {
		name    string
		view    string // "" = gh fails
		wantErr string // "" = success
	}{
		{"same repo", view("false", "feature", "main"), ""},
		{"fork", view("true", "main", "main"), "cross-repository"},
		{"fork, different branch", view("true", "feature", "main"), "cross-repository"},
		{"head is base", view("false", "main", "main"), "head branch is its base branch"},
		{"gh error", "", "gh pr view 7 failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newScriptEnv(t)
			if tc.view == "" {
				e.set("FAKE_GH_FAIL=1")
			} else {
				f := filepath.Join(e.dir, "view.json")
				write(t, f, tc.view)
				e.set("FAKE_GH_VIEW=" + f)
			}
			out, errOut, code := e.run(t, reviewPRScript(t, "fetch-pr.sh"), "7")
			if tc.wantErr != "" {
				if code == 0 {
					t.Fatalf("fetch-pr.sh succeeded, want failure %q: %q", tc.wantErr, out)
				}
				if !strings.Contains(errOut, tc.wantErr) {
					t.Errorf("stderr %q, want it to contain %q", errOut, tc.wantErr)
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			var got map[string]string
			if err := json.Unmarshal([]byte(lastLine(out)), &got); err != nil {
				t.Fatalf("output %q: %v", out, err)
			}
			want := map[string]string{"vcs": "git", "pr_url": "https://github.com/o/r/pull/7", "head_sha": "abc123",
				"base_branch": "main", "branch": "feature", "title": "a title with spaces"}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
