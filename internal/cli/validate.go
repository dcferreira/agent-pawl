package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// cmdValidate implements pawl validate <workflow-name> | --path <file>
// (design/format-spec.md §H, §I): resolve the workflow — by name under
// .claude/workflows/ (the ordinary case, resolveWorkflowFile) or, with
// --path, a specific file instead, skipping name resolution entirely — run
// the static checks, print every error plus the soft: census, and exit
// non-zero on any error. Whenever the workflow declares one or more
// guards:, it also prints a guards: line through the same writeGuardsLine
// helper pawl run's banner uses (internal/cli/format.go) — with neutral
// wording, "guards: N declared (enforced only when a run starts with the
// pawl hooks installed)", since validate has no run whose enforcement
// could be on or off: validate is
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
var validateCmd = &command{
	name:   "validate",
	usage:  cmdUsage(usageValidate),
	flags:  []flagDef{{name: "path"}},
	maxPos: 1,
}

func cmdValidate(args []string, cwd string, stdout, stderr io.Writer) int {
	pf, code, done := validateCmd.parseOrExit(args, stdout, stderr)
	if done {
		return code
	}
	path, havePath := pf.value("path"), pf.has("path")
	var name string
	haveName := len(pf.pos) > 0
	if haveName {
		name = pf.pos[0]
	}
	if haveName && havePath {
		fmt.Fprintln(stderr, "usage: pawl validate <workflow-name> | --path <file> — a name and --path are mutually exclusive")
		return exitUsage
	}
	if !haveName && !havePath {
		fmt.Fprintln(stderr, "usage: pawl validate <workflow-name> | --path <file>")
		return exitUsage
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
			return exitNotFound
		}
		rw = &resolvedWorkflow{Path: abs, Source: "path"}
	} else {
		var err error
		rw, err = resolveWorkflowFile(cwd, name)
		if err != nil {
			msg := err.Error()
			// pawl validate (unlike pawl run) takes --path, so a
			// path-looking name is almost certainly a file the author
			// meant to pass that way (issue #26).
			if looksLikePath(name) && strings.Contains(msg, "no workflow named") {
				abs := name
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(cwd, abs)
				}
				stem := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
				if fileExists(abs) {
					msg += "; did you mean: `pawl validate --path " + render.ShellQuote(name) + "`"
				} else if stem != name {
					if _, serr := resolveWorkflowFile(cwd, stem); serr == nil {
						msg += "; did you mean: `pawl validate " + render.ShellQuote(stem) + "`"
					}
				}
			}
			printLine(stderr, msg)
			return exitNotFound
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

	// Rule 16 (referenced files exist) needs the filesystem, so it is not in
	// spec.Validate; its errors are reported exactly like Validate's.
	report.Errors = append(report.Errors, spec.CheckFiles(wf)...)

	w := &blockWriter{}
	w.line(0, "workflow:", rw.Path, "("+rw.Source+")")
	writeGuardsLine(w, len(wf.Guards), guardsValidate)
	writeSoftCensus(w, report)
	fmt.Fprint(stdout, w.String())

	// Validator errors go to stderr, so a script can capture them apart
	// from the workflow/guards/census lines on stdout.
	if len(report.Errors) > 0 {
		eb := &blockWriter{}
		for _, e := range report.Errors {
			eb.line(0, e)
		}
		fmt.Fprint(stderr, eb.String())
		// the frozen exit-code table (exitcode.go): "validation failed" is
		// its own clause under 2 (usage/validation), not 1 (resolution) —
		// the file resolved fine; spec.Validate is what refused it.
		return exitUsage
	}
	return exitOK
}
