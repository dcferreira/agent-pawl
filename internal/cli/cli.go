// Package cli implements the pawl commands — run, submit, status, abandon,
// validate, list — and the stdout grammar the /pawl skill drives against
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
	"strings"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

// Run dispatches on args[1] and returns the process exit code. It is the
// single entry point exercised by every test but hook_test.go: no path
// through it calls os.Exit, so every command is testable by capturing
// stdout/stderr. It is RunIO fed an empty stdin, for the commands that
// never read it.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunIO(args, strings.NewReader(""), stdout, stderr)
}

// RunIO is Run plus a stdin reader for the one command that needs it:
// `pawl hook pre|stop` reads a Claude Code hook payload from stdin.
func RunIO(args []string, stdin io.Reader, stdout, stderr io.Writer) (exit int) {
	defer RecoverPanic(stderr, &exit)
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[1] {
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return exitOK
	}
	cwd, err := os.Getwd()
	if err != nil {
		// An environment error, not a missing run or workflow.
		printLine(stderr, "pawl: resolving working directory:", err.Error())
		return exitInternal
	}
	// hook runs its own fail-open recover and calls commandHook itself.
	if commandHook != nil && args[1] != "hook" {
		commandHook(args[1])
	}
	var code int
	switch args[1] {
	case "run":
		code = cmdRun(args[2:], cwd, stdout, stderr)
	case "submit":
		code = cmdSubmit(args[2:], cwd, stdout, stderr)
	case "poll":
		code = cmdPoll(args[2:], cwd, stdout, stderr)
	case "status":
		return cmdStatus(args[2:], cwd, stdout, stderr)
	case "abandon":
		code = cmdAbandon(args[2:], cwd, stdout, stderr)
	case "validate":
		return cmdValidate(args[2:], cwd, stdout, stderr)
	case "list":
		return cmdList(args[2:], cwd, stdout, stderr)
	case "hook":
		return cmdHook(args[2:], stdin, stdout, stderr)
	default:
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	// Best-effort: a refused run has nothing to link, a failed submit/poll
	// still leaves the index consistent with what actually happened, and a
	// sync failure here never changes the command's own exit code — it is
	// only bin/pawl-hook's fast-path cache (journal.Live(root) remains the
	// authority on which runs are live).
	if root, err := journal.ResolveRoot(cwd); err == nil {
		_ = journal.SyncLiveIndex(root)
	}
	return code
}
