// Command pawl is a state-machine workflow engine for Claude Code.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/dcferreira/agent-pawl/internal/cli"
)

// Version is the pawl binary version. It is overridden at build time with
// -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run dispatches on args[1] and returns the process exit code. It is the
// single entry point exercised by tests; main is a thin wrapper around it.
// "version" and "update" are handled here, since they are the only
// commands that need the build-time Version string ("update" needs it to
// know what it's updating from, and to refuse to overwrite a source/
// go-install build without --force — see internal/cli.CmdUpdate); every
// other command — including the usage text printed for no/unknown
// subcommand — is internal/cli.Run's single source of truth, so it is not
// duplicated here.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) >= 2 && args[1] == "version" {
		fmt.Fprintf(stdout, "pawl %s\n", Version)
		return 0
	}
	if len(args) >= 2 && args[1] == "update" {
		return cli.CmdUpdate(args[2:], stdout, stderr, Version, nil)
	}
	return cli.RunIO(args, os.Stdin, stdout, stderr)
}
