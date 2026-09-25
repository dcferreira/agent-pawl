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

// usage is the single source of truth for pawl's command-line surface
// (design/format-spec.md §I): cmd/pawl's own run() prints this same text for
// every command but "version" and "update", which it handles itself so a
// build-time version string need not flow through this package (see
// internal/cli/update.go's CmdUpdate doc comment).
const usage = `Usage: pawl <command> [args]

Commands:
  pawl run <name> [key=value …] [--fresh] [--force] [--run <id>] [--no-enforcement]
        start, or resume a non-terminal run
  pawl validate <name> | --path <file>
        run the static checks against a workflow file (--path validates a
        specific file instead of a name resolved under .claude/workflows/)
  pawl status [--run <id>] [--json]
        show where a run is, and its trust surface (--json: machine-readable)
  pawl abandon --run <id> [--reason <text>]
        abandon a run; always available, always terminal
  pawl list
        list resolvable workflows and their source
  pawl submit --run <id> --step <id> --json '<result>'
        internal: submit an agentic step's result (the /pawl skill calls this;
        an author never writes it)
  pawl poll --run <id> --step <name>
        internal: poll a wait step until it resolves, then submit for itself
        (the /pawl skill runs this under Monitor; an author never writes it)
  pawl hook pre|stop
        internal: Claude Code hook entry point (PreToolUse / Stop); reads the
        hook payload on stdin. Wired by the plugin's hooks/hooks.json.
  pawl version
        print the pawl version
  pawl update [--check] [--version <vX.Y.Z>] [--force]
        self-update to the latest (or a pinned) GitHub release binary;
        refuses to overwrite a source/go-install ("dev") build without
        --force
`

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
func RunIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		printLine(stderr, "pawl: resolving working directory:", err.Error())
		return 1
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
		return 2
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
