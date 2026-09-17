// Package cli implements the wf commands — run, submit, status, abandon,
// validate, list — and the stdout grammar the /wf skill drives against
// (DESIGN.md §2): the run-start banner, the DISPATCH block and the TERMINAL
// line. internal/engine returns every decision as a value (engine.Dispatch,
// engine.Terminal); turning that into the printed contract with the model is
// this package's job and nobody else's.
//
// cli depends on internal/engine (and, transitively, spec/render/emit/
// journal) and on nothing else internal.
package cli

import (
	"fmt"
	"io"
	"os"
)

// usage is the single source of truth for wf's command-line surface
// (design/format-spec.md §I): cmd/wf's own run() prints this same text for
// every command but "version", which it handles itself so a build-time
// version string need not flow through this package.
const usage = `Usage: wf <command> [args]

Commands:
  wf run <name> [key=value …] [--fresh] [--force] [--run <id>]
        start, or resume a non-terminal run
  wf validate <name>
        run the static checks against a workflow file
  wf status [--run <id>]
        show where a run is, and its trust surface
  wf abandon --run <id>
        abandon a run; always available, always terminal
  wf list
        list resolvable workflows and their source
  wf submit --run <id> --step <id> --json '<result>'
        internal: submit an agentic step's result (the /wf skill calls this;
        an author never writes it)
  wf version
        print the wf version
`

// Run dispatches on args[1] and returns the process exit code. It is the
// single entry point exercised by tests: no path through it calls
// os.Exit, so every command is testable by capturing stdout/stderr.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		printLine(stderr, "wf: resolving working directory:", err.Error())
		return 1
	}
	switch args[1] {
	case "run":
		return cmdRun(args[2:], cwd, stdout, stderr)
	case "submit":
		return cmdSubmit(args[2:], cwd, stdout, stderr)
	case "status":
		return cmdStatus(args[2:], cwd, stdout, stderr)
	case "abandon":
		return cmdAbandon(args[2:], cwd, stdout, stderr)
	case "validate":
		return cmdValidate(args[2:], cwd, stdout, stderr)
	case "list":
		return cmdList(args[2:], cwd, stdout, stderr)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}
