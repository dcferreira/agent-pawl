package cli

import (
	"strings"
	"testing"
)

const twoStepBlockedWorkflow = `workflow: hello
start: greet
args:
  name: {type: string, required: true}
state:
  note: {type: string, default: "a"}
steps:
  - id: greet
    kind: deterministic
    run: echo hello from ${name} > hello.txt
    postcondition: test -s nothing.txt
    next: count
  - id: count
    kind: deterministic
    run: wc -l < hello.txt
    postcondition: test -f hello.txt
    next: done
terminal: {done: {status: ok, message: "Wrote hello.txt for ${name}."}}
`

// resumeRefusalDetail starts a run of twoStepBlockedWorkflow (it blocks at
// greet, leaving a live run), rewrites the workflow file with edit, resumes,
// and returns the text inside "(changed: ...)" of the refusal.
func resumeRefusalDetail(t *testing.T, edit func(string) string) string {
	t.Helper()
	root := setupWorkingCopy(t)
	writeWorkflow(t, root, "hello", twoStepBlockedWorkflow)
	if _, stderr, code := runCLI(t, []string{"pawl", "run", "hello", "name=ada"}); code != 3 {
		t.Fatalf("first run: exit %d, want 3 (blocked); stderr = %q", code, stderr)
	}
	writeWorkflow(t, root, "hello", edit(twoStepBlockedWorkflow))
	_, stderr, code := runCLI(t, []string{"pawl", "run", "hello"})
	if code != 4 {
		t.Fatalf("resume: exit %d, want 4; stderr = %q", code, stderr)
	}
	const marker = "(changed: "
	i := strings.Index(stderr, marker)
	if i < 0 {
		t.Fatalf("stderr has no changed detail: %q", stderr)
	}
	rest := stderr[i+len(marker):]
	j := strings.Index(rest, "); use")
	if j < 0 {
		t.Fatalf("unterminated changed detail: %q", stderr)
	}
	return rest[:j]
}

func TestResumeRefusal_NamesOnlyEditedStep(t *testing.T) {
	got := resumeRefusalDetail(t, func(s string) string {
		return strings.Replace(s, "echo hello from", "echo hi from", 1)
	})
	if got != "greet" {
		t.Errorf("changed detail = %q, want %q", got, "greet")
	}
}

func TestResumeRefusal_NonStepChangeSaysOutsideSteps(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"state default":    func(s string) string { return strings.Replace(s, `default: "a"`, `default: "b"`, 1) },
		"terminal message": func(s string) string { return strings.Replace(s, "Wrote hello.txt", "Wrote the file", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			got := resumeRefusalDetail(t, edit)
			if got != "workflow-level fields only (start:, args:, state:, terminal:, …)" {
				t.Errorf("changed detail = %q, want %q", got, "workflow-level fields only (start:, args:, state:, terminal:, …)")
			}
		})
	}
}

func TestResumeRefusal_AddedAndRemovedSteps(t *testing.T) {
	added := resumeRefusalDetail(t, func(s string) string {
		s = strings.Replace(s, "    next: done\nterminal:", "    next: extra\nterminal:", 1)
		return strings.Replace(s, "terminal:", "  - id: extra\n    kind: deterministic\n    run: \"true\"\n    postcondition: \"true\"\n    next: done\nterminal:", 1)
	})
	if added != "count, extra (added)" {
		t.Errorf("added detail = %q, want %q", added, "count, extra (added)")
	}
	removed := resumeRefusalDetail(t, func(s string) string {
		s = strings.Replace(s, "    next: count\n", "    next: done\n", 1)
		i := strings.Index(s, "  - id: count")
		j := strings.Index(s, "terminal:")
		return s[:i] + s[j:]
	})
	if removed != "count (removed), greet" {
		t.Errorf("removed detail = %q, want %q", removed, "count (removed), greet")
	}
}

func TestResumeRefusal_ReorderedStepsSaysOrderChanged(t *testing.T) {
	got := resumeRefusalDetail(t, func(s string) string {
		i := strings.Index(s, "  - id: greet")
		j := strings.Index(s, "  - id: count")
		k := strings.Index(s, "terminal:")
		return s[:i] + s[j:k] + s[i:j] + s[k:]
	})
	if got != "step order changed" {
		t.Errorf("reorder detail = %q, want %q", got, "step order changed")
	}
}
