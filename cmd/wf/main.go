// Command wf is a state-machine workflow engine for Claude Code.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/dcferreira/agentic-workflow-fsm/internal/cli"
)

// Version is the wf binary version. It is overridden at build time with
// -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run dispatches on args[1] and returns the process exit code. It is the
// single entry point exercised by tests; main is a thin wrapper around it.
// "version" is handled here, since it is the only command that needs the
// build-time Version string; every other command — including the usage
// text printed for no/unknown subcommand — is internal/cli.Run's single
// source of truth, so it is not duplicated here.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) >= 2 && args[1] == "version" {
		fmt.Fprintf(stdout, "wf %s\n", Version)
		return 0
	}
	return cli.Run(args, stdout, stderr)
}
