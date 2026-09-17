package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dcferreira/agentic-workflow-fsm/internal/render"
)

// TestResolveScriptPathTemplate covers the four cases DESIGN.md §9
// distinguishes: a bare command name (found via PATH, untouched), a
// relative path with a separator (resolved against the workflow file's
// directory), an explicit "./" relative path (same), and an absolute path
// (untouched) — plus the ${key}-contributes-the-first-token variants, which
// must be decided from the template (pre-render), not from rendered/quoted
// output.
func TestResolveScriptPathTemplate(t *testing.T) {
	const dir = "/work/wf-dir"
	tests := []struct {
		name string
		tmpl string
		vals render.Values
		want string
	}{
		{
			name: "bare command name found via PATH is untouched",
			tmpl: "ruff format .",
			want: "ruff format .",
		},
		{
			name: "relative path with separator resolves against workflow dir",
			tmpl: "scripts/run-tests.sh --foo",
			want: render.ShellQuote("/work/wf-dir/scripts/run-tests.sh") + " --foo",
		},
		{
			name: "explicit ./ relative path resolves against workflow dir",
			tmpl: "./check.sh",
			want: render.ShellQuote("/work/wf-dir/check.sh"),
		},
		{
			name: "absolute path is untouched",
			tmpl: "/usr/bin/foo --bar",
			want: "/usr/bin/foo --bar",
		},
		{
			name: "whole first token from a ${key} substitution resolves",
			tmpl: "${script} --foo",
			vals: render.Values{"script": render.StringValue("scripts/run-tests.sh")},
			want: render.ShellQuote("/work/wf-dir/scripts/run-tests.sh") + " --foo",
		},
		{
			name: "first token mixing literal text and a ${key} resolves",
			tmpl: "scripts/${name}.sh ${arg}",
			vals: render.Values{
				"name": render.StringValue("run-tests"),
				"arg":  render.StringValue("x y"),
			},
			want: render.ShellQuote("/work/wf-dir/scripts/run-tests.sh") + " " + render.ShellQuote("x y"),
		},
		{
			name: "absolute path from a ${key} substitution is untouched (still quoted)",
			tmpl: "${bin} --bar",
			vals: render.Values{"bin": render.StringValue("/usr/bin/foo")},
			want: render.ShellQuote("/usr/bin/foo") + " --bar",
		},
		{
			name: "bare command name from a ${key} substitution is untouched (still quoted)",
			tmpl: "${bin} format .",
			vals: render.Values{"bin": render.StringValue("ruff")},
			want: render.ShellQuote("ruff") + " format .",
		},
		{
			name: "empty command",
			tmpl: "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveScriptPathTemplate(tt.tmpl, tt.vals, dir)
			if err != nil {
				t.Fatalf("resolveScriptPathTemplate(%q) unexpected error: %v", tt.tmpl, err)
			}
			if got != tt.want {
				t.Errorf("resolveScriptPathTemplate(%q, dir=%q) = %q, want %q", tt.tmpl, dir, got, tt.want)
			}
		})
	}
}

// TestResolveScriptPathTemplate_InjectionRegression is round 1's finding,
// pinned as a regression test: a first token built entirely from a ${key}
// substitution whose value contains an unbalanced single quote plus a
// command substitution ($(...)) must never let that value reach unquoted
// shell context. The old implementation rendered+quoted the whole line
// first, then tried to relocate/re-quote the first token by dequoting
// rendered text; render.ShellQuote's per-quote escaping ('\”) desynced
// that scanner after an odd number of embedded quotes, so the tail of the
// value (including "$(...)") ended up outside any quoting. This version
// decides on the un-rendered template, so the value only ever passes
// through render.ShellQuote once and never through a second, hand-rolled
// parser. Asserted at both layers: the string this function returns, and
// (in TestExecDeterministic_InjectionAttemptDoesNotExecute) that actually
// running it does not perform the side effect.
func TestResolveScriptPathTemplate_InjectionRegression(t *testing.T) {
	const dir = "/work/wf-dir"
	malicious := "x'/y $(touch PWNED)'"
	vals := render.Values{"tool": render.StringValue(malicious)}

	got, err := resolveScriptPathTemplate("${tool}", vals, dir)
	if err != nil {
		t.Fatalf("resolveScriptPathTemplate: unexpected error: %v", err)
	}
	// The whole malicious value must be inside exactly one
	// render.ShellQuote call around the resolved path — never split, and
	// never reachable in unquoted shell context.
	want := render.ShellQuote(filepath.Join(dir, malicious))
	if got != want {
		t.Fatalf("resolveScriptPathTemplate(%q) = %q, want %q (the whole malicious value quoted as one token)", malicious, got, want)
	}
}

// TestResolveScriptPathTemplate_BenignApostropheIsNotAnError guards the
// other symptom round 1 reported: an ordinary value containing a single
// apostrophe (no injection attempt at all) must resolve cleanly, not error
// out as though a quote were unterminated.
func TestResolveScriptPathTemplate_BenignApostropheIsNotAnError(t *testing.T) {
	const dir = "/work/wf-dir"
	vals := render.Values{"p": render.StringValue("a'b/c d")}
	got, err := resolveScriptPathTemplate("${p}", vals, dir)
	if err != nil {
		t.Fatalf("resolveScriptPathTemplate: unexpected error on benign apostrophe input: %v", err)
	}
	want := render.ShellQuote(filepath.Join(dir, "a'b/c d"))
	if got != want {
		t.Errorf("resolveScriptPathTemplate = %q, want %q", got, want)
	}
}

// TestExecDeterministic_ScriptResolvesAgainstWorkflowFileNotRoot is the case
// the brief flags as currently untested and the one where the old ("cwd at
// the working-copy root") and new ("relative to the workflow file")
// behaviours actually differ: the workflow file's directory here is not
// e.Root (newTestEngine already places them in two separate temp dirs), and
// the script referenced by run: exists only beside the workflow file, not
// under e.Root. If resolution fell back to the old cwd-based behaviour (or
// stayed unimplemented), "sh" would fail to find ./mark.sh and the step
// would not reach its marker-file side effect in e.Root.
func TestExecDeterministic_ScriptResolvesAgainstWorkflowFileNotRoot(t *testing.T) {
	const yaml = `
workflow: script-path
start: a
steps:
  - id: a
    kind: deterministic
    run: ./mark.sh
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)

	// The script lives beside the workflow file (e.Workflow.Path's
	// directory), which newTestEngine already made distinct from e.Root.
	workflowDir := filepath.Dir(e.Workflow.Path)
	scriptPath := filepath.Join(workflowDir, "mark.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ntouch marker\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if workflowDir == e.Root {
		t.Fatal("test setup invariant broken: workflow file directory equals e.Root, so this case would not distinguish old from new behaviour")
	}

	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	term, ok := instr.(Terminal)
	if !ok || term.Status != "ok" {
		t.Fatalf("got %+v, want Terminal{Status: ok} (./mark.sh must have run successfully via cwd=e.Root)", instr)
	}

	// The script's own side effect (touch marker, run with cwd=e.Root per
	// §3, which this change does not alter) proves it actually executed,
	// not merely that "sh" found nothing to run and exited 0 some other way.
	if _, err := os.Stat(filepath.Join(e.Root, "marker")); err != nil {
		t.Errorf("expected ./mark.sh to have run with cwd=e.Root and created marker there: %v", err)
	}
}

// TestExecDeterministic_InjectionAttemptDoesNotExecute is round 1's
// end-to-end proof requirement: a real Engine, a real step, a real
// arg-provided value containing "'" and "$(...)" in first-token position —
// asserting the side effect (a file created by the embedded command
// substitution) does not happen, not merely that the rendered string looks
// safe.
func TestExecDeterministic_InjectionAttemptDoesNotExecute(t *testing.T) {
	const yaml = `
workflow: injection-guard
start: a
args:
  tool: {type: string, required: true}
steps:
  - id: a
    kind: deterministic
    run: ${tool}
    next: done
terminal: {done: {status: ok}}
`
	e := newTestEngine(t, yaml)
	malicious := "x'/y $(touch PWNED)'"

	// The step is expected to fail (there is no such file to execute) —
	// what matters is what does NOT happen, checked below.
	_, _ = e.Start("run1", map[string]any{"tool": malicious})

	if _, err := os.Stat(filepath.Join(e.Root, "PWNED")); err == nil {
		t.Fatal("SECURITY: the injected $(touch PWNED) executed — command injection via a run: first token")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.Workflow.Path), "PWNED")); err == nil {
		t.Fatal("SECURITY: the injected $(touch PWNED) executed (in the workflow dir) — command injection via a run: first token")
	}
}
