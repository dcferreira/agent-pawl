// Command pawl is a state-machine workflow engine for Claude Code.
package main

import (
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
func run(args []string, stdout, stderr io.Writer) (code int) {
	defer cli.RecoverPanic(stderr, &code)
	cli.Version = Version
	if len(args) >= 2 && args[1] == "version" {
		return cli.CmdVersion(args[2:], stdout, stderr, Version)
	}
	// Top-level --version / -v are aliases for `pawl version`, only as the
	// first argument; `pawl <command> --version` is unchanged.
	if len(args) >= 2 && (args[1] == "--version" || args[1] == "-v") {
		return cli.CmdVersion(args[2:], stdout, stderr, Version)
	}
	if len(args) >= 2 && args[1] == "update" {
		return cli.CmdUpdate(args[2:], stdout, stderr, Version, nil)
	}
	return cli.RunIO(args, os.Stdin, stdout, stderr)
}
