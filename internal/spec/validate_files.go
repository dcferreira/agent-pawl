package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/render"
)

// CheckFiles implements design/format-spec.md §H rule 16: every literal path
// a workflow references must exist on disk right now. It is deliberately NOT
// part of Validate, which stays filesystem-free: rule 16 depends on the
// filesystem at the moment it runs, so `pawl validate` and `pawl run`
// starting a NEW run call it, and a resume, `pawl status` or a hook never
// does (a script deleted after its step already ran must not make a live run
// unresumable).
//
// Checked: every step's run:, poll:, postcondition command:, every
// invariant's check:, and every context: entry. (A parallel branch or a
// foreach body is an ordinary top-level step, so walking w.Steps covers it.)
// A command reference is checked only when its first token — split by
// render.FirstTokenTemplate, the same split the runtime makes
// (internal/engine/scriptpath.go) — is a literal path: non-empty, no "${",
// and containing "/" (a bare name is a PATH lookup). It must be a regular
// file with an execute bit. A plain (non-!cmd) context entry is a whole-value
// file path, which must be a readable regular file, not necessarily
// executable.
//
// Paths resolve exactly as at runtime: absolute as-is, otherwise relative to
// the directory of w.Path, never cwd. With w.Path == "" there is no base and
// nil is returned. Errors are in declaration order, one per offending
// reference.
//
// The check runs before any step does, so a literal path that an earlier step
// creates during the run is rejected; such a file must be referenced through
// ${key} (skipped here) or read via !cmd.
func CheckFiles(w *Workflow) []string {
	if w == nil || w.Path == "" {
		return nil
	}
	base := filepath.Dir(w.Path)
	var errs []string

	cmdRef := func(where, tmpl string) {
		first, _ := render.FirstTokenTemplate(tmpl)
		if first == "" || strings.Contains(first, "${") || !strings.Contains(first, "/") {
			return
		}
		if msg := checkPath(base, first, true, !filepath.IsAbs(first)); msg != "" {
			errs = append(errs, fmt.Sprintf("%s: %s %q (resolved to %s) %s", w.Path, where, first, resolveRef(base, first), msg))
		}
	}

	for i := range w.Steps {
		s := &w.Steps[i]
		at := fmt.Sprintf("step %q: rule 16", s.ID)
		if s.Run != "" {
			cmdRef(at+": run:", s.Run)
		}
		if s.Poll != "" {
			cmdRef(at+": poll:", s.Poll)
		}
		if s.Postcondition != nil && s.Postcondition.Command != "" {
			cmdRef(at+": postcondition.command:", s.Postcondition.Command)
		}
		for j, c := range s.Context {
			where := fmt.Sprintf("%s: context[%d]:", at, j)
			if c.IsCmd {
				cmdRef(where, c.Value)
				continue
			}
			if c.Value == "" || strings.Contains(c.Value, "${") {
				continue
			}
			if msg := checkPath(base, c.Value, false, !filepath.IsAbs(c.Value)); msg != "" {
				errs = append(errs, fmt.Sprintf("%s: %s %q (resolved to %s) %s", w.Path, where, c.Value, resolveRef(base, c.Value), msg))
			}
		}
	}
	for _, inv := range w.Invariants {
		cmdRef(fmt.Sprintf("invariant %q: rule 16: check:", inv.ID), inv.Check)
	}
	return errs
}

func resolveRef(base, tok string) string {
	if filepath.IsAbs(tok) {
		return tok
	}
	return filepath.Join(base, tok)
}

// checkPath returns "" when tok resolves to an acceptable file, else the
// "<what>; <how to fix>" tail of the error message. relative says the token
// was written relative (so the fix hint may mention the workflow-directory
// resolution rule).
func checkPath(base, tok string, wantExec, relative bool) string {
	p := resolveRef(base, tok)
	fi, err := os.Stat(p) // follows symlinks
	if err != nil {
		if os.IsNotExist(err) {
			if relative {
				return "does not exist; create it there or fix the path (paths resolve relative to the workflow file's directory)"
			}
			return "does not exist; create it there or fix the path"
		}
		return "cannot be read: " + err.Error() + "; fix its permissions or the path"
	}
	if fi.IsDir() {
		return "is a directory; point at a file instead"
	}
	if !fi.Mode().IsRegular() {
		return "is not a regular file; point at a regular file instead"
	}
	if wantExec {
		if fi.Mode()&0o111 == 0 {
			return "is not executable; chmod +x it"
		}
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return "is not readable: " + err.Error() + "; fix its permissions or the path"
	}
	_ = f.Close()
	return ""
}
