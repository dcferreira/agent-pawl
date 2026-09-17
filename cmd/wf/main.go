// Command wf is a state-machine workflow engine for Claude Code.
package main

import (
	"fmt"
	"io"
	"os"
)

// Version is the wf binary version. It is overridden at build time with
// -ldflags "-X main.Version=...".
var Version = "dev"

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
  wf version
        print the wf version
`

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run dispatches on args[1] and returns the process exit code. It is the
// single entry point exercised by tests; main is a thin wrapper around it.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[1] {
	case "version":
		fmt.Fprintf(stdout, "wf %s\n", Version)
		return 0
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}
