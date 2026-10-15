package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// One shared flag parser for every pawl command, so a script sees the same
// rules everywhere (frozen with the 1.0 CLI contract):
//
//   - a value flag takes `--f v` or `--f=v`; an empty value or a missing one
//     is a usage error (a flag may opt in to an empty value: allowEmpty);
//   - a bool flag takes no value, `--f=x` is a usage error;
//   - a flag given twice is a usage error, not last-wins;
//   - any other argument starting with "-" is a usage error (no single-dash
//     forms, no "--" terminator);
//   - -h/--help anywhere prints the command's usage to stdout and exits 0;
//   - a value flag consumes the next argument whatever it looks like, so
//     `--reason -h` is a value, not a help request.
//
// stdlib flag is deliberately not used: it stops at the first positional,
// accepts single-dash forms and is last-wins.

type flagDef struct {
	name       string // without the leading "--"
	isBool     bool
	allowEmpty bool // value flags only: an empty value is legitimate
}

type command struct {
	name   string
	usage  string // printed for --help (stdout) and after a usage error (stderr)
	flags  []flagDef
	maxPos int // maximum positionals; -1 = unlimited
}

type parsedFlags struct {
	vals map[string]string
	pos  []string
}

func (p *parsedFlags) has(name string) bool { _, ok := p.vals[name]; return ok }
func (p *parsedFlags) value(name string) string {
	return p.vals[name]
}
func (p *parsedFlags) boolean(name string) bool { return p.has(name) }

// errHelp is returned by parse when -h/--help was given.
var errHelp = errors.New("help requested")

func (c *command) def(name string) *flagDef {
	for i := range c.flags {
		if c.flags[i].name == name {
			return &c.flags[i]
		}
	}
	return nil
}

// parse returns errHelp, or an error whose text is the one-line usage
// problem (without the "pawl <cmd>:" prefix).
func (c *command) parse(args []string) (*parsedFlags, error) {
	// Help first, so it wins over any other bad argument. A value flag's
	// space-form value is skipped: `--reason -h` is a value.
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-h" || a == "--help" {
			return nil, errHelp
		}
		if strings.HasPrefix(a, "--") && !strings.Contains(a, "=") {
			if d := c.def(a[2:]); d != nil && !d.isBool {
				i++
			}
		}
	}
	p := &parsedFlags{vals: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			if c.maxPos >= 0 && len(p.pos) >= c.maxPos {
				return nil, fmt.Errorf("unrecognised argument %q", a)
			}
			p.pos = append(p.pos, a)
			continue
		}
		if !strings.HasPrefix(a, "--") {
			return nil, fmt.Errorf("unrecognised argument %q", a)
		}
		name, val, hasEq := strings.Cut(a[2:], "=")
		d := c.def(name)
		if d == nil {
			return nil, fmt.Errorf("unrecognised argument %q", a)
		}
		if d.isBool {
			if hasEq {
				return nil, fmt.Errorf("--%s does not take a value", name)
			}
		} else if !hasEq {
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--%s needs a value", name)
			}
			val = args[i]
		}
		if !d.isBool && val == "" && !d.allowEmpty {
			return nil, fmt.Errorf("--%s needs a non-empty value", name)
		}
		if p.has(name) {
			return nil, fmt.Errorf("--%s given more than once", name)
		}
		p.vals[name] = val
	}
	return p, nil
}

// fail prints a one-line usage problem plus the command's usage to stderr.
func (c *command) fail(stderr io.Writer, msg string) int {
	printLine(stderr, "pawl "+c.name+":", msg)
	fmt.Fprint(stderr, c.usage)
	return exitUsage
}

// parseOrExit runs parse and renders its outcome: done is true when the
// caller must return code (help, or a usage error).
func (c *command) parseOrExit(args []string, stdout, stderr io.Writer) (p *parsedFlags, code int, done bool) {
	p, err := c.parse(args)
	switch {
	case err == errHelp:
		fmt.Fprint(stdout, c.usage)
		return nil, exitOK, true
	case err != nil:
		return nil, c.fail(stderr, err.Error()), true
	}
	return p, exitOK, false
}

// Per-command usage blocks: the one source for both `pawl <cmd> --help` and
// the top-level usage text.
const (
	usageRun = `  pawl run <name> [key=value …] [--fresh] [--force] [--run <id>] [--no-enforcement]
        start, or resume a non-terminal run
`
	usageValidate = `  pawl validate <name> | --path <file>
        run the static checks against a workflow file (--path validates a
        specific file instead of a name resolved under .claude/workflows/)
`
	usageStatus = `  pawl status [--run <id>] [--json]
        show where a run is, and its trust surface (--json: machine-readable)
`
	usageAbandon = `  pawl abandon --run <id> [--reason <text>]
        abandon a run; always available, always terminal
`
	usageList = `  pawl list
        list resolvable workflows and their source
`
	usageSubmit = `  pawl submit --run <id> --step <id> [--item <n>] --json '<result>'
        internal: submit an agentic step's result (the /pawl skill calls this;
        an author never writes it)
`
	usagePoll = `  pawl poll --run <id> --step <name>
        internal: poll a wait step until it resolves, then submit for itself
        (the /pawl skill runs this under Monitor; an author never writes it)
`
	usageHook = `  pawl hook pre|stop|subagent-stop
        internal: Claude Code hook entry point (PreToolUse / Stop / SubagentStop);
        reads the hook payload on stdin. Wired by the plugin's hooks/hooks.json.
`
	usageVersion = `  pawl version | --version | -v
        print the pawl version
`
	usageUpdate = `  pawl update [--check] [--version <vX.Y.Z>] [--force]
        self-update to the latest (or a pinned) GitHub release binary;
        refuses to overwrite a source/go-install ("dev") build without
        --force
`
)

// usage is the full text for `pawl help` and for a missing/unknown command.
const usage = "Usage: pawl <command> [args]\n\nCommands:\n" +
	usageRun + usageValidate + usageStatus + usageAbandon + usageList +
	usageSubmit + usagePoll + usageHook + usageVersion + usageUpdate +
	"\nRun pawl <command> --help for one command's usage.\n"

func cmdUsage(block string) string { return "Usage:\n" + block }

// CmdVersion implements `pawl version`; main.go owns the build-time string.
func CmdVersion(args []string, stdout, stderr io.Writer, version string) int {
	c := &command{name: "version", usage: cmdUsage(usageVersion)}
	if _, code, done := c.parseOrExit(args, stdout, stderr); done {
		return code
	}
	fmt.Fprintf(stdout, "pawl %s\n", version)
	return exitOK
}
