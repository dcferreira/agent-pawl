package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// cmdValidate implements pawl validate <workflow-name> [--path <file>]
// (design/format-spec.md §H, §I): resolve the workflow — by name under
// .claude/workflows/ (the ordinary case, resolveWorkflowFile) or, with
// --path, a specific file instead, skipping name resolution entirely — run
// the static checks, print every error plus the soft: census, and exit
// non-zero on any error. Whenever the workflow declares one or more
// guards:, it also prints the same "guards: N declared, NOT enforced (no
// PreToolUse hook in this build)" line pawl run's banner prints (through
// the shared writeGuardsLine helper — internal/cli/format.go): validate is
// the command an author runs while writing guards:, and reporting the file
// as clean without a word about enforcement would be exactly the
// silent-acceptance failure Ruling R8 exists to prevent.
//
// --path and a positional name are mutually exclusive: an author validating
// a file mid-edit, before it is even placed under .claude/workflows/ (or
// checked out under a different name than they mean to give it once it
// lands there), has no name to give pawl for it, and accepting both while
// silently preferring one would hide exactly the kind of discrepancy this
// codebase refuses elsewhere rather than reports (see cmdRun's
// --fresh/--run refusal).
//
// Every printed value goes through a blockWriter (fix round 5): spec's
// validator error messages interpolate author text — a description, a
// ${key} reference — with %s rather than %q in several rules, so a
// workflow crafted to include a column-0-shaped line in that text used to
// reach stdout raw. blockWriter.line's whole-line singleLine guard closes
// this without any change to spec: it does not matter which rule's message
// carried the hostile text, or whether that rule happens to quote its
// interpolation correctly.
func cmdValidate(args []string, cwd string, stdout, stderr io.Writer) int {
	var name, path string
	var haveName, havePath bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--path":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, "pawl validate: --path needs a value")
				return 2
			}
			if havePath {
				// A repeated --path used to silently keep the last value —
				// the same "hide a discrepancy instead of reporting it"
				// failure the name/--path mutual-exclusion check below
				// exists to refuse (reviewer finding 10).
				printLine(stderr, "pawl validate: --path given more than once")
				return 2
			}
			path = args[i]
			havePath = true
		case strings.HasPrefix(arg, "-"):
			// Any other argument starting with "-" — a bad flag, a typo, or
			// --strict (documented once, never implemented — see below) —
			// is never treated as a positional name, in either argument
			// order. It used to fall through to the name branch below,
			// so `pawl validate --strict` looked up a workflow literally
			// named "--strict" and failed with a resolution error (exit 1)
			// instead of the usage error (exit 2) an unrecognised flag is
			// everywhere else in this package (reviewer finding 9).
			printLine(stderr, "pawl validate: unrecognised argument", arg)
			return 2
		default:
			if haveName {
				printLine(stderr, "pawl validate: unrecognised argument", arg)
				return 2
			}
			name = arg
			haveName = true
		}
	}
	if haveName && havePath {
		fmt.Fprintln(stderr, "usage: pawl validate <workflow-name> [--path <file>] — a name and --path are mutually exclusive")
		return 2
	}
	if !haveName && !havePath {
		fmt.Fprintln(stderr, "usage: pawl validate <workflow-name> [--path <file>]")
		return 2
	}

	var rw *resolvedWorkflow
	if havePath {
		// A relative --path resolves against cwd, exactly like a relative
		// workflow name resolves against .claude/workflows/ under cwd —
		// neither one is ever interpreted relative to some other directory
		// pawl happens to know about.
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		if !fileExists(abs) {
			printLine(stderr, fmt.Sprintf("pawl: no workflow file at %q", abs))
			return 1
		}
		rw = &resolvedWorkflow{Path: abs, Source: "path"}
	} else {
		var err error
		rw, err = resolveWorkflowFile(cwd, name)
		if err != nil {
			printLine(stderr, err.Error())
			return 1
		}
	}

	// loadAndValidate resolves scripts/context relative to rw.Path's own
	// directory (resolveScriptPathTemplate, execShell's context gathering)
	// exactly the same way for a --path file as for a name-resolved one:
	// nothing downstream of rw.Path branches on how it was found.
	wf, report, err := loadAndValidate(rw.Path)
	if err != nil {
		printLine(stderr, err.Error())
		return exitForLoadErr(err)
	}

	w := &blockWriter{}
	w.line(0, "workflow:", rw.Path, "("+rw.Source+")")
	writeGuardsLine(w, len(wf.Guards))
	for _, e := range report.Errors {
		w.line(0, e)
	}
	writeSoftCensus(w, report)
	fmt.Fprint(stdout, w.String())

	if len(report.Errors) > 0 {
		// docs/cli.md's exit-code table (~line 33): "validation failed" is
		// its own clause under 2 (usage/validation), not 1 (resolution) —
		// the file resolved fine; spec.Validate is what refused it.
		return 2
	}
	return 0
}
