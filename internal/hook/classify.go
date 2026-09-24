package hook

import (
	"path/filepath"
	"strings"
)

// segments splits a shell command on the control operators that start a new
// simple command. It does not understand quoting: the classification is
// advisory string matching, like guards (DESIGN.md §5).
func segments(cmd string) [][]string {
	r := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "&", "\n")
	var out [][]string
	for _, seg := range strings.Split(r.Replace(cmd), "\n") {
		f := strings.Fields(seg)
		for len(f) > 0 && isAssignment(f[0]) {
			f = f[1:]
		}
		if len(f) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// isAssignment reports whether tok looks like a leading shell variable
// assignment (FOO=1 cmd ...), so it can be skipped when finding the command.
func isAssignment(tok string) bool {
	i := strings.IndexByte(tok, '=')
	return i > 0 && !strings.ContainsAny(tok[:i], "/-.")
}

// IsPawlCommand reports whether any segment of cmd invokes pawl: the first
// token of the segment has basename "pawl".
func IsPawlCommand(cmd string) bool {
	for _, f := range segments(cmd) {
		if filepath.Base(f[0]) == "pawl" {
			return true
		}
	}
	return false
}

// IsVCSMutation reports whether any segment of cmd is a mutating git or jj
// command.
func IsVCSMutation(cmd string) bool {
	for _, f := range segments(cmd) {
		switch filepath.Base(f[0]) {
		case "git":
			if gitMutates(f[1:]) {
				return true
			}
		case "jj":
			if jjMutates(f[1:]) {
				return true
			}
		}
	}
	return false
}

func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// stripGlobal drops leading global options, including the value of any
// option in withValue given as a separate token.
func stripGlobal(args []string, withValue map[string]bool) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if withValue[args[0]] && len(args) > 1 {
			args = args[2:]
			continue
		}
		args = args[1:]
	}
	return args
}

var gitValueOpts = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true}

var gitMutating = map[string]bool{
	"commit": true, "push": true, "pull": true, "reset": true, "checkout": true, "switch": true,
	"restore": true, "merge": true, "rebase": true, "cherry-pick": true, "revert": true,
	"tag": true, "branch": true, "stash": true, "am": true, "apply": true, "add": true,
	"rm": true, "mv": true, "clean": true,
}

// gitMutates classifies a git invocation (args with "git" already stripped)
// as mutating or read-only.
func gitMutates(args []string) bool {
	if hasHelp(args) {
		return false
	}
	args = stripGlobal(args, gitValueOpts)
	if len(args) == 0 || !gitMutating[args[0]] {
		return false
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "branch", "tag":
		// Listing forms: no positional argument, only list-ish flags.
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") {
				return true
			}
			switch a {
			case "-l", "--list", "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "-n":
			default:
				return true
			}
		}
		return false
	case "stash":
		return !(len(rest) > 0 && (rest[0] == "list" || rest[0] == "show"))
	}
	return true
}

var jjValueOpts = map[string]bool{
	"-R": true, "--repository": true, "--at-op": true, "--at-operation": true,
	"--color": true, "--config": true, "--config-toml": true, "--config-file": true,
}

var jjReadOnly = map[string]bool{
	"log": true, "st": true, "status": true, "diff": true, "show": true, "evolog": true,
	"obslog": true, "root": true, "help": true, "interdiff": true, "version": true,
}

var jjReadOnlySub = map[string]map[string]bool{
	"file":      {"show": true, "list": true, "annotate": true, "search": true},
	"op":        {"log": true, "show": true, "diff": true},
	"operation": {"log": true, "show": true, "diff": true},
	"workspace": {"root": true, "list": true},
	"bookmark":  {"list": true, "l": true},
	"b":         {"list": true, "l": true},
	"config":    {"get": true, "list": true, "path": true},
}

// jjMutates classifies a jj invocation (args with "jj" already stripped) as
// mutating or read-only. Unrecognized subcommands default to mutating.
func jjMutates(args []string) bool {
	if hasHelp(args) {
		return false
	}
	args = stripGlobal(args, jjValueOpts)
	if len(args) == 0 || jjReadOnly[args[0]] {
		return false
	}
	if subs, ok := jjReadOnlySub[args[0]]; ok {
		return !(len(args) > 1 && subs[args[1]])
	}
	if args[0] == "git" && len(args) > 2 && args[1] == "remote" && args[2] == "list" {
		return false
	}
	return true
}
