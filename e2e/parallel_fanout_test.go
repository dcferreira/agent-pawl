package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stubReadme has exactly 4 lines; stubDesign has the "## 1. Purpose" and
// "## 2" headings branch_c's context command slices between. Both are STUBS
// standing in for the repository's own README.md / DESIGN.md, which the
// example reads from the run cwd.
const (
	stubReadme      = "# stub\n\nline three\nline four\n"
	stubReadmeLines = "4"
	stubDesign      = "# Design stub\n\n## 1. Purpose\nStub purpose text.\n\n## 2. Next\nOther.\n"
)

// TestParallelFanout runs "docs/examples/parallel-fanout": branch_a
// (deterministic) is resolved by the engine, running a real wc -l over the
// stub README; branch_b and branch_c (agentic) are dispatched together in one
// DISPATCH_PARALLEL block. Both agentic submits below are CANNED: no live
// subagent or LLM ran.
func TestParallelFanout(t *testing.T) {
	src := filepath.Join(repoRoot(t), filepath.FromSlash("docs/examples/parallel-fanout"))
	dir := t.TempDir()
	copyFile(t, filepath.Join(src, "workflow.yaml"), mustMkdirFile(t, filepath.Join(dir, ".claude", "workflows"), "parallel-fanout.yaml"), 0o644)
	for name, content := range map[string]string{"README.md": stubReadme, "DESIGN.md": stubDesign} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := &project{root: dir, stateDir: t.TempDir()}

	out, code := p.run(t, "run", "parallel-fanout")
	if code != 0 {
		t.Fatalf("pawl run: exit %d\n%s", code, out)
	}
	if n := strings.Count(out, "DISPATCH_PARALLEL "); n != 2 { // opening + END line
		t.Fatalf("want exactly one DISPATCH_PARALLEL block, got %d marker lines:\n%s", n, out)
	}
	for _, want := range []string{"branch_b", "branch_c", "Stub purpose text."} {
		if !strings.Contains(out, want) {
			t.Errorf("DISPATCH_PARALLEL block missing %q:\n%s", want, out)
		}
	}
	if regexp.MustCompile(`(?m)^\s*DISPATCH \S+ branch_a`).MatchString(out) {
		t.Errorf("branch_a is deterministic and must not be dispatched:\n%s", out)
	}
	m := regexp.MustCompile(`(?m)^DISPATCH_PARALLEL (\S+) fanout`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no DISPATCH_PARALLEL for fanout:\n%s", out)
	}
	run := m[1]

	// CANNED submits for both agentic branches.
	out, code = p.run(t, "submit", "--run", run, "--step", "branch_b", "--json", `{"summary_b":"Canned summary of the stub README."}`)
	if code != 0 {
		t.Fatalf("submit branch_b: exit %d\n%s", code, out)
	}
	out, code = p.run(t, "submit", "--run", run, "--step", "branch_c", "--json", `{"summary_c":"Canned summary of the stub purpose."}`)
	if code != 0 {
		t.Fatalf("submit branch_c: exit %d\n%s", code, out)
	}
	if m := terminalLine.FindStringSubmatch(out); m == nil || m[1] != "ok" {
		t.Fatalf("expected TERMINAL ok, got:\n%s", out)
	}
	if !strings.Contains(out, "README.md is "+stubReadmeLines+" lines") {
		t.Fatalf("terminal message should carry the real wc -l count (%s):\n%s", stubReadmeLines, out)
	}
}
