package cli

import (
	"fmt"
	"io"
)

// cmdValidate implements wf validate <name> (design/format-spec.md §H,
// §I): resolve the workflow, run the static checks, print every error plus
// the soft: census, and exit non-zero on any error.
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
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: wf validate <name>")
		return 2
	}
	name := args[0]

	rw, err := resolveWorkflowFile(cwd, name)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}
	_, report, err := loadAndValidate(rw.Path)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}

	w := &blockWriter{}
	w.line(0, "workflow:", rw.Path, "("+rw.Source+")")
	for _, e := range report.Errors {
		w.line(0, e)
	}
	writeSoftCensus(w, report)
	fmt.Fprint(stdout, w.String())

	if len(report.Errors) > 0 {
		return 1
	}
	return 0
}
