package engine

import (
	"path/filepath"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/render"
)

// resolveScriptPathTemplate implements DESIGN.md §9's "scripts/ resolve
// relative to the workflow file" rule for a run:, postcondition command:, or
// !cmd context template, and returns the fully rendered command line.
//
// The decision is made on the author's template, via
// render.FirstTokenTemplate, strictly before any ${key} substitution
// reaches a shell-quoting decision — never by re-parsing already-rendered,
// already-quoted output. An earlier version of this function rendered the
// whole line first and then tried to relocate and re-quote the first token
// by dequoting it; render.ShellQuote's escaping ('\” per embedded quote)
// desynchronised that hand-rolled scanner after an odd number of quotes, so
// a value like x'/y $(touch PWNED)' came back believed-unquoted and its
// tail landed in live shell context — a command-injection hole, proven end
// to end. Deciding on the template instead means a value can only ever end
// up inside exactly one render.ShellQuote call, so there is no rendered
// text left to re-parse.
//
// If the first token is a literal (contains no "${"), the path rule
// applies to it directly. If it contains "${", its keys are substituted
// raw and unquoted (render.RenderRaw) to get the candidate path string —
// safe, because that candidate is only ever used to test-and-join a path
// and is then shell-quoted exactly once (render.ShellQuote) before
// reaching the command line; it is never concatenated into it unquoted.
// The remainder of the template renders exactly as before, via
// render.RenderShell.
func resolveScriptPathTemplate(tmpl string, vals render.Values, dir string) (string, error) {
	first, rest := render.FirstTokenTemplate(tmpl)
	if first == "" {
		return render.RenderShell(tmpl, vals)
	}

	literal := first
	if strings.Contains(first, "${") {
		raw, err := render.RenderRaw(first, vals)
		if err != nil {
			return "", err
		}
		literal = raw
	}

	if literal == "" || !containsPathSeparator(literal) || filepath.IsAbs(literal) {
		return render.RenderShell(tmpl, vals)
	}

	resolved := filepath.Join(dir, literal)
	renderedRest, err := render.RenderShell(rest, vals)
	if err != nil {
		return "", err
	}
	return render.ShellQuote(resolved) + renderedRest, nil
}

// containsPathSeparator reports whether s has a "/" anywhere in it — the
// signal (per DESIGN.md §9) that a first token is a path rather than a bare
// command name meant to be found via PATH.
func containsPathSeparator(s string) bool {
	return strings.Contains(s, "/")
}
