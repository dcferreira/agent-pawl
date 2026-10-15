package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkFile(t *testing.T, dir, rel string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func detStep(id, run string) Step { return Step{ID: id, Kind: "deterministic", Run: run} }

func TestCheckFiles(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, dir, "scripts/ok.sh", 0o755)
	mkFile(t, dir, "scripts/noexec.sh", 0o644)
	mkFile(t, dir, "NOTES.md", 0o644)
	mkFile(t, dir, "secret.md", 0o000)
	if err := os.MkdirAll(filepath.Join(dir, "scripts/adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "scripts/ok.sh"), filepath.Join(dir, "scripts/link.sh")); err != nil {
		t.Fatal(err)
	}
	abs := mkFile(t, t.TempDir(), "abs.sh", 0o755)
	wfPath := filepath.Join(dir, "wf.yaml")

	tests := []struct {
		name string
		w    *Workflow
		want []string // substrings, one per expected error, in order
	}{
		{"relative ok", &Workflow{Steps: []Step{detStep("a", "scripts/ok.sh --x")}}, nil},
		{"missing", &Workflow{Steps: []Step{detStep("test", "scripts/nope.sh")}},
			[]string{`step "test": rule 16: run: "scripts/nope.sh" (resolved to ` + filepath.Join(dir, "scripts/nope.sh") + `) does not exist`}},
		{"non-executable", &Workflow{Steps: []Step{detStep("a", "scripts/noexec.sh")}},
			[]string{`step "a": rule 16: run: "scripts/noexec.sh" (resolved to ` + filepath.Join(dir, "scripts/noexec.sh") + `) is not executable; chmod +x it`}},
		{"directory", &Workflow{Steps: []Step{detStep("a", "scripts/adir")}},
			[]string{`step "a": rule 16: run: "scripts/adir" (resolved to ` + filepath.Join(dir, "scripts/adir") + `) is a directory`}},
		{"absolute ok", &Workflow{Steps: []Step{detStep("a", abs)}}, nil},
		{"absolute missing", &Workflow{Steps: []Step{detStep("a", "/nonexistent-pawl-dir/x.sh")}},
			[]string{`step "a": rule 16: run: "/nonexistent-pawl-dir/x.sh" (resolved to /nonexistent-pawl-dir/x.sh) does not exist`}},
		{"bare command skipped", &Workflow{Steps: []Step{detStep("a", "go test ./...")}}, nil},
		{"var first token skipped", &Workflow{Steps: []Step{detStep("a", "${x}/scripts/a.sh")}}, nil},
		{"var inside first token skipped", &Workflow{Steps: []Step{detStep("a", "scripts/${x}.sh")}}, nil},
		{"empty skipped", &Workflow{Steps: []Step{detStep("a", "")}}, nil},
		{"symlink ok", &Workflow{Steps: []Step{detStep("a", "scripts/link.sh")}}, nil},
		{"plain context ok", &Workflow{Steps: []Step{{ID: "a", Kind: "agentic", Context: []ContextEntry{{Value: "NOTES.md"}}}}}, nil},
		{"plain context missing", &Workflow{Steps: []Step{{ID: "a", Kind: "agentic", Context: []ContextEntry{{Value: "NOTES.md"}, {Value: "NOPE.md"}}}}},
			[]string{`step "a": rule 16: context[1]: "NOPE.md" (resolved to ` + filepath.Join(dir, "NOPE.md") + `) does not exist`}},
		{"plain context unreadable", &Workflow{Steps: []Step{{ID: "a", Kind: "agentic", Context: []ContextEntry{{Value: "secret.md"}}}}},
			[]string{`step "a": rule 16: context[0]: "secret.md"`}},
		{"plain context with var skipped", &Workflow{Steps: []Step{{ID: "a", Kind: "agentic", Context: []ContextEntry{{Value: "${x}.md"}}}}}, nil},
		{"plain context not exec ok", &Workflow{Steps: []Step{{ID: "a", Kind: "agentic", Context: []ContextEntry{{Value: "scripts/noexec.sh"}}}}}, nil},
		{"cmd context missing", &Workflow{Steps: []Step{{ID: "a", Kind: "agentic", Context: []ContextEntry{{IsCmd: true, Value: "scripts/nope.sh arg"}}}}},
			[]string{`step "a": rule 16: context[0]: "scripts/nope.sh" (resolved to ` + filepath.Join(dir, "scripts/nope.sh") + `) does not exist`}},
		{"cmd context non-exec", &Workflow{Steps: []Step{{ID: "a", Kind: "agentic", Context: []ContextEntry{{IsCmd: true, Value: "scripts/noexec.sh"}}}}},
			[]string{`step "a": rule 16: context[0]: "scripts/noexec.sh" (resolved to ` + filepath.Join(dir, "scripts/noexec.sh") + `) is not executable; chmod +x it`}},
		{"invariant check", &Workflow{Invariants: []InvariantDecl{{ID: "i", Check: "scripts/nope.sh"}}},
			[]string{`invariant "i": rule 16: check: "scripts/nope.sh" (resolved to ` + filepath.Join(dir, "scripts/nope.sh") + `) does not exist`}},
		{"poll", &Workflow{Steps: []Step{{ID: "s", Kind: "wait", Poll: "scripts/nope.sh"}}},
			[]string{`step "s": rule 16: poll: "scripts/nope.sh"`}},
		{"postcondition command", &Workflow{Steps: []Step{{ID: "s", Kind: "agentic", Postcondition: &Postcondition{Command: "scripts/noexec.sh", HasCommand: true}}}},
			[]string{`step "s": rule 16: postcondition.command: "scripts/noexec.sh"`}},
		{"foreach body", &Workflow{Steps: []Step{
			{ID: "fan", Kind: "parallel", Foreach: &Foreach{Over: "xs", Body: "body", Collect: "r"}},
			detStep("body", "scripts/nope.sh")}},
			[]string{`step "body": rule 16: run: "scripts/nope.sh"`}},
		{"multiple collected", &Workflow{
			Steps:      []Step{detStep("a", "scripts/n1.sh"), detStep("b", "scripts/noexec.sh")},
			Invariants: []InvariantDecl{{ID: "i", Check: "scripts/n2.sh"}}},
			[]string{`step "a": rule 16: run: "scripts/n1.sh"`, `step "b": rule 16: run: "scripts/noexec.sh"`, `invariant "i": rule 16: check: "scripts/n2.sh"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "plain context unreadable" {
				// root (e.g. a dev container) can open a 0o000 file, so the
				// case can't be produced there.
				if f, err := os.Open(filepath.Join(dir, "secret.md")); err == nil {
					f.Close()
					t.Skip("0o000 file is readable (running as root?)")
				}
			}
			tt.w.Path = wfPath
			got := CheckFiles(tt.w)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d errors %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if !strings.Contains(got[i], tt.want[i]) {
					t.Errorf("error %d = %q, want it to contain %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestCheckFiles_Shape pins the documented one-line shape shared with the
// other rules: <file>: step "<id>": rule 16: <what>; <how to fix>.
func TestCheckFiles_Shape(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, dir, "scripts/noexec.sh", 0o644)
	wfPath := filepath.Join(dir, "wf.yaml")
	w := &Workflow{Path: wfPath, Steps: []Step{
		{ID: "fix", Kind: "agentic", Postcondition: &Postcondition{Command: "scripts/nope.sh", HasCommand: true}},
		detStep("b", "scripts/noexec.sh"),
	}}
	got := CheckFiles(w)
	want := []string{
		wfPath + `: step "fix": rule 16: postcondition.command: "scripts/nope.sh" (resolved to ` + filepath.Join(dir, "scripts/nope.sh") +
			`) does not exist; create it there or fix the path (paths resolve relative to the workflow file's directory)`,
		wfPath + `: step "b": rule 16: run: "scripts/noexec.sh" (resolved to ` + filepath.Join(dir, "scripts/noexec.sh") +
			`) is not executable; chmod +x it`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("error %d =\n%q\nwant\n%q", i, got[i], want[i])
		}
	}
}

func TestCheckFiles_EmptyPathReturnsNil(t *testing.T) {
	w := &Workflow{Steps: []Step{detStep("a", "scripts/nope.sh")}}
	if got := CheckFiles(w); got != nil {
		t.Errorf("got %q, want nil", got)
	}
}

func TestCheckFiles_ResolvesAgainstWorkflowDirNotCwd(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, dir, "scripts/ok.sh", 0o755)
	yaml := "workflow: w\nstart: a\nsteps:\n  - id: a\n    kind: deterministic\n    run: scripts/ok.sh\n    next: done\nterminal:\n  done: {status: ok, message: m}\n"
	p := filepath.Join(dir, "wf.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if got := CheckFiles(w); len(got) != 0 {
		t.Errorf("from other cwd: %q, want none", got)
	}
	if err := os.Remove(filepath.Join(dir, "scripts/ok.sh")); err != nil {
		t.Fatal(err)
	}
	if got := CheckFiles(w); len(got) != 1 {
		t.Errorf("after delete: %q, want 1 error", got)
	}
}

// A context file an earlier step writes during the run does not exist when
// CheckFiles runs (before any step), so a literal reference to it is rejected;
// referencing it through ${key} or a bare !cmd is the documented way past rule 16.
func TestCheckFiles_ContextCreatedByEarlierStep(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, dir, "scripts/plan.sh", 0o755)
	steps := func(ctx ContextEntry) []Step {
		return []Step{detStep("plan", "scripts/plan.sh"), {ID: "act", Kind: "agentic", Context: []ContextEntry{ctx}}}
	}
	literal := &Workflow{Path: filepath.Join(dir, "wf.yaml"), Steps: steps(ContextEntry{Value: "plan.md"})}
	if got := CheckFiles(literal); len(got) != 1 || !strings.Contains(got[0], `step "act": rule 16: context[0]: "plan.md"`) {
		t.Errorf("literal: got %q, want one plan.md does-not-exist error", got)
	}
	for _, ctx := range []ContextEntry{{Value: "${plan_file}"}, {IsCmd: true, Value: "cat plan.md"}} {
		w := &Workflow{Path: filepath.Join(dir, "wf.yaml"), Steps: steps(ctx)}
		if got := CheckFiles(w); len(got) != 0 {
			t.Errorf("%+v: got %q, want none", ctx, got)
		}
	}
}

func TestValidate_IsFilesystemFree(t *testing.T) {
	dir := t.TempDir()
	yaml := "workflow: w\nstart: a\nsteps:\n  - id: a\n    kind: deterministic\n    run: scripts/missing.sh\n    next: done\nterminal:\n  done: {status: ok, message: m}\n"
	p := filepath.Join(dir, "wf.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Validate(w)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Errors {
		if strings.Contains(e, "does not exist") {
			t.Errorf("Validate reported a filesystem error: %q", e)
		}
	}
}
