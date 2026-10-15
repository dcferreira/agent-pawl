package e2e

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var askRunID = regexp.MustCompile(`(?m)^ASK\s+(\S+)\s+ask_approval`)

// newHumanApprovalProject lays "docs/examples/human-approval" out as a named
// workflow and copies its NOTES.md next to the workflow YAML: the engine
// resolves agentic context files (the default doc_path "NOTES.md") relative
// to the workflow file's directory, not the run cwd.
func newHumanApprovalProject(t *testing.T) *project {
	t.Helper()
	src := filepath.Join(repoRoot(t), filepath.FromSlash("docs/examples/human-approval"))
	dir := t.TempDir()
	wf := filepath.Join(dir, ".claude", "workflows")
	copyFile(t, filepath.Join(src, "workflow.yaml"), mustMkdirFile(t, wf, "human-approval.yaml"), 0o644)
	copyFile(t, filepath.Join(src, "NOTES.md"), filepath.Join(wf, "NOTES.md"), 0o644)
	return &project{root: dir, stateDir: t.TempDir()}
}

// toAsk runs the example, answers the agentic summarize step with a CANNED
// submit (no live subagent or LLM ran), and returns the run id once the
// human ask_approval step has printed its ASK block.
func toAsk(t *testing.T, p *project) string {
	t.Helper()
	out, code := p.run(t, "run", "human-approval")
	if code != 0 || !strings.Contains(out, "DISPATCH") || !strings.Contains(out, "summarize") {
		t.Fatalf("pawl run: exit %d, want a summarize DISPATCH:\n%s", code, out)
	}
	run := extractRunID(t, out)
	// CANNED submit: the summary text is supplied by this test.
	out, code = p.run(t, "submit", "--run", run, "--step", "summarize",
		"--json", `{"summary":"Canned summary of the example notes."}`)
	if code != 0 {
		t.Fatalf("submit summarize: exit %d\n%s", code, out)
	}
	m := askRunID.FindStringSubmatch(out)
	if m == nil || m[1] != run || !strings.Contains(out, "Canned summary of the example notes.") || !strings.Contains(out, "Approve this summary?") {
		t.Fatalf("expected an ASK for ask_approval carrying the summary, got:\n%s", out)
	}
	return run
}

// TestHumanApprovalApprove: the human answer "approve" routes through record
// to the ok terminal, and writes: [decision] captures the chosen label.
func TestHumanApprovalApprove(t *testing.T) {
	p := newHumanApprovalProject(t)
	run := toAsk(t, p)
	// The human answer is also scripted: this test plays the person.
	out, code := p.run(t, "submit", "--run", run, "--step", "ask_approval", "--json", `{"selected":["approve"]}`)
	if code != 0 {
		t.Fatalf("answer: exit %d\n%s", code, out)
	}
	if m := terminalLine.FindStringSubmatch(out); m == nil || m[1] != "ok" || !strings.Contains(out, "Approved: approve") {
		t.Fatalf("expected TERMINAL ok with \"Approved: approve\", got:\n%s", out)
	}
}

// TestHumanApprovalRevise: "revise" routes to the blocked terminal (exit 3).
func TestHumanApprovalRevise(t *testing.T) {
	p := newHumanApprovalProject(t)
	run := toAsk(t, p)
	out, code := p.run(t, "submit", "--run", run, "--step", "ask_approval", "--json", `{"selected":["revise"]}`)
	if code != 3 {
		t.Fatalf("answer: exit %d, want 3 (blocked)\n%s", code, out)
	}
	if m := terminalLine.FindStringSubmatch(out); m == nil || m[1] != "blocked" || !strings.Contains(out, "Needs revision or timed out") {
		t.Fatalf("expected TERMINAL blocked, got:\n%s", out)
	}
}
