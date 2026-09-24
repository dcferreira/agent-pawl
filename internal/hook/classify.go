package hook

import (
	"path/filepath"
	"strings"
)

// segments splits a shell command into simple commands on the control
// operators ; & | && || and newline, each as its words with quotes removed
// and leading env assignments and reserved words skipped (see analyze). It
// is a small, quote-aware lexer for a deliberately narrow shell subset, not a
// shell parser — the classification stays advisory string matching, like
// guards (DESIGN.md §5):
//   - a separator inside single or double quotes, or backslash-escaped
//     outside single quotes, is part of the word, not a segment boundary
//     (so a `pawl submit --json '{"summary":"a; b"}'` stays one segment);
//   - & directly after > or < (2>&1, >&2, N<&M) or directly before >
//     (&>file), and | directly after > (>|file), are redirections that
//     stay in the current segment, not background/pipe operators;
//   - a backslash-newline outside single quotes is a line continuation.
//
// Anything outside that subset makes the lexer report unsure rather than
// guess (see lex), and callers must then fail safe.
func segments(cmd string) [][]string {
	segs, _, _ := analyze(cmd)
	return segs
}

// lex splits cmd into raw segments (words with quotes removed; nothing
// stripped) and reports:
//
// subst — cmd contains a command or process substitution ($( or a backtick
// outside single quotes, <( or >( outside any quotes), which runs an
// arbitrary command wherever it appears, even inside another command's
// argument;
//
// unsure — cmd uses a construct outside the subset the lexer models, so its
// segments can't be trusted to match what the shell will run: a quote left
// unterminated at end of input, an unquoted # that starts a word (a comment,
// whose apostrophes would otherwise open a bogus quote), $'...' ANSI-C
// quoting (where \' does not close the quote), a heredoc/herestring (<<),
// a subshell or function definition (an unquoted ( starting a word, or ()),
// or brace expansion (an unquoted { in a word longer than {, other than ${).
// This is the scriptpath.go lesson (AGENTS.md): when the lexer's state could
// have drifted from the shell's, say so instead of classifying a desynced
// token stream.
func lex(cmd string) (segs [][]string, subst, unsure bool) {
	var seg []string
	var word strings.Builder
	inWord := false
	var quote byte // 0, '\'', or '"'
	var prev byte  // previous unquoted character of the current word, 0 if none
	endWord := func() {
		if inWord {
			seg = append(seg, word.String())
		}
		word.Reset()
		inWord, prev = false, 0
	}
	endSeg := func() {
		endWord()
		if len(seg) > 0 {
			segs = append(segs, seg)
		}
		seg = nil
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		next := byte(0)
		if i+1 < len(cmd) {
			next = cmd[i+1]
		}
		switch quote {
		case '\'':
			if c == '\'' {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		case '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && next == '\n':
				i++ // line continuation
			case c == '\\' && strings.IndexByte("\"\\$`", next) >= 0:
				word.WriteByte(next)
				i++
			default:
				if c == '`' || (c == '$' && next == '(') {
					subst = true
				}
				word.WriteByte(c)
			}
			continue
		}
		switch {
		case c == '#' && !inWord:
			unsure = true // a comment: stop trusting the rest
			word.WriteByte(c)
			inWord, prev = true, c
		case c == '$' && next == '\'':
			unsure = true // ANSI-C quoting, not modelled
			word.WriteByte(c)
			inWord, prev = true, c
		case c == '\'' || c == '"':
			quote, inWord, prev = c, true, 0
		case c == '\\':
			if next == '\n' {
				i++ // line continuation: neither character is part of a word
				continue
			}
			if next != 0 {
				word.WriteByte(next)
				i++
			}
			inWord, prev = true, 0
		case c == ' ' || c == '\t':
			endWord()
		case c == '\n' || c == ';':
			endSeg()
		case c == '&' && (prev == '>' || prev == '<' || next == '>'),
			c == '|' && prev == '>':
			word.WriteByte(c)
			inWord, prev = true, c
		case c == '&' || c == '|':
			endSeg()
		default:
			switch {
			case c == '`' || ((c == '$' || c == '<' || c == '>') && next == '('):
				subst = true
			case c == '<' && next == '<', // heredoc / herestring
				c == '(' && (!inWord || next == ')'), // subshell, f() / f ()
				c == '{' && inWord && prev != '$',    // brace expansion
				c == '{' && !inWord && next != 0 && strings.IndexByte(" \t\n", next) < 0:
				unsure = true
			}
			word.WriteByte(c)
			inWord, prev = true, c
		}
	}
	if quote != 0 {
		unsure = true // unterminated quote at end of input
	}
	endSeg()
	return segs, subst, unsure
}

// leadingReserved are shell reserved words and grouping tokens that may
// precede a simple command in the same segment (`do git add x`,
// `then git push`, `{ git commit; }`, `! git commit`, `time git push`);
// they are stripped so the real command word is classified.
var leadingReserved = map[string]bool{
	"do": true, "then": true, "else": true, "elif": true, "if": true, "while": true,
	"until": true, "!": true, "{": true, "time": true,
}

// unsureStarters begin compound commands whose bodies this lexer does not
// model (case patterns like `a) git push;;`, function definitions).
var unsureStarters = map[string]bool{"case": true, "select": true, "function": true, "coproc": true}

// wrappers run another command given as their arguments (or as a string),
// so the segment's own first word says nothing about what executes.
var wrappers = map[string]bool{
	"eval": true, "exec": true, "command": true, "builtin": true, "env": true, "sudo": true,
	"doas": true, "su": true, "nohup": true, "nice": true, "ionice": true, "timeout": true,
	"xargs": true, "find": true, "parallel": true, "watch": true, "stdbuf": true, "chroot": true,
	"unshare": true, "flock": true, "strace": true, "script": true, "time": true, "busybox": true,
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"source": true, ".": true,
}

// analyze is lex plus per-segment normalization: leading env assignments
// and reserved words are stripped until the command word is reached. A
// segment whose command word is a wrapper, starts an unmodelled compound
// command, or is itself an expansion or glob ($G, g*t) also marks the
// command unsure.
func analyze(cmd string) (segs [][]string, subst, unsure bool) {
	raw, subst, unsure := lex(cmd)
	for _, seg := range raw {
		for len(seg) > 0 {
			switch {
			case isAssignment(seg[0]), leadingReserved[seg[0]]:
				if seg[0] == "time" && len(seg) > 1 && seg[1] == "-p" {
					seg = seg[1:]
				}
				seg = seg[1:]
				continue
			}
			break
		}
		if len(seg) == 0 {
			continue
		}
		base := filepath.Base(seg[0])
		if wrappers[base] || unsureStarters[seg[0]] || strings.ContainsAny(seg[0], "$*?[") {
			unsure = true
		}
		segs = append(segs, seg)
	}
	return segs, subst, unsure
}

// isAssignment reports whether tok looks like a leading shell variable
// assignment (FOO=1 cmd ...), so it can be skipped when finding the command.
func isAssignment(tok string) bool {
	i := strings.IndexByte(tok, '=')
	return i > 0 && !strings.ContainsAny(tok[:i], "/-.")
}

// IsPawlCommand reports whether any segment of cmd invokes pawl: the first
// token of the segment has basename "pawl". It is deliberately permissive
// (it ignores unsure): a false positive only writes a heartbeat.
func IsPawlCommand(cmd string) bool {
	for _, f := range segments(cmd) {
		if filepath.Base(f[0]) == "pawl" {
			return true
		}
	}
	return false
}

// IsPawlOnlyCommand reports whether cmd invokes only pawl (and, since
// dispatching a run change directory first is routine, cd): every segment's
// first token (after env assignments) must be pawl or cd, not just some
// segment, and cmd must contain no command or process substitution. This is
// the narrower, safe exemption from DecidePre's fail-closed rule for a run
// whose guards could not be loaded: IsPawlCommand
// alone is too wide, since `pawl status; rm -rf x` has a pawl segment but
// also an unrelated, unchecked one that must still be denied — as does
// `pawl run wf msg=$(rm -rf x)`, whose substitution runs before pawl does.
// Separators inside a quoted argument (a `pawl submit --json` summary) and
// fd redirects like 2>&1 do not start a new segment (see segments). It fails
// safe: if the lexer is unsure (a comment, $'...', an unterminated quote, a
// heredoc, ... — see lex/analyze), cmd is never pawl-only.
func IsPawlOnlyCommand(cmd string) bool {
	segs, subst, unsure := analyze(cmd)
	if len(segs) == 0 || subst || unsure {
		return false
	}
	for _, f := range segs {
		base := filepath.Base(f[0])
		if base != "pawl" && base != "cd" {
			return false
		}
	}
	return true
}

// IsVCSMutation reports whether any segment of cmd is a mutating git or jj
// command. When the lexer is unsure, or cmd contains a substitution (whose
// inner command the segments don't expose), it fails safe to rawVCSMutation:
// a false deny for a subagent's odd command is acceptable, a false allow is
// not.
func IsVCSMutation(cmd string) bool {
	segs, subst, unsure := analyze(cmd)
	if subst || unsure {
		return rawVCSMutation(cmd)
	}
	for _, f := range segs {
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

// rawVCSMutation is the conservative fallback for a command the lexer can't
// vouch for: it ignores shell structure entirely, removing quote and
// backslash characters (so g"i"t and g\it still read as git) and splitting
// on whitespace and every other shell metacharacter. Each git or jj token is
// classified by the same default-deny rule as a parsed segment, applied to
// every token after it — but a --help/-h anywhere later is ignored here,
// since without shell structure it may belong to a different command
// (`git push $(ls -h)`).
func rawVCSMutation(cmd string) bool {
	cleaned := strings.NewReplacer("'", "", `"`, "", `\`, "").Replace(cmd)
	toks := strings.FieldsFunc(cleaned, func(r rune) bool {
		return r <= ' ' || strings.ContainsRune(";&|()<>`$#{},=!", r)
	})
	for i, t := range toks {
		switch filepath.Base(t) {
		case "git":
			if gitArgsMutate(toks[i+1:]) {
				return true
			}
		case "jj":
			if jjArgsMutate(toks[i+1:]) {
				return true
			}
		}
	}
	return false
}

// helpOnly reports whether args (a git or jj invocation with global options
// already stripped, args[0] the subcommand) only asks for help: -h/--help
// directly follows the subcommand. A -h/--help anywhere later may be an
// option's value (`git commit -m -h` commits with message "-h"), so it
// earns no exemption. A bare `git --help`/`jj -h` leaves nothing after
// stripGlobal and is read-only without this check.
func helpOnly(args []string) bool {
	return len(args) >= 2 && (args[1] == "--help" || args[1] == "-h")
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

// gitReadOnly are git subcommands that never write refs, the index, the
// working tree or config. Every other subcommand defaults to mutating
// (gitArgsMutate), the same default-deny rule jj gets: a subagent's odd
// read-only command being denied is recoverable, a ref rewrite
// (update-ref, worktree add, fetch, ...) slipping through is not.
var gitReadOnly = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "rev-parse": true,
	"ls-files": true, "ls-tree": true, "ls-remote": true, "blame": true, "grep": true,
	"describe": true, "cat-file": true, "for-each-ref": true, "merge-base": true,
	"shortlog": true, "name-rev": true, "count-objects": true, "help": true, "version": true,
}

// gitConfigReadFlags make `git config` a pure read.
var gitConfigReadFlags = map[string]bool{
	"--get": true, "--get-all": true, "--get-regexp": true, "--get-urlmatch": true,
	"--list": true, "-l": true,
}

// branchListFlags are git branch/tag flags that only list, filter or format
// existing refs — never create, delete, move or rename one. A flag not in
// this set (e.g. -d/-D delete, -m/-M move, -f force-create) defaults to
// mutating, matching the default-deny rule for every other git and jj
// subcommand.
var branchListFlags = map[string]bool{
	"-l": true, "--list": true, "-a": true, "--all": true, "-r": true, "--remotes": true,
	"-v": true, "-vv": true, "--verbose": true,
	"--show-current": true, "--contains": true, "--no-contains": true,
	"--merged": true, "--no-merged": true, "--points-at": true,
	"--format": true, "--sort": true,
	"--column": true, "--no-column": true, "--color": true, "--no-color": true,
}

// branchListValueFlags may take their value as a separate following token
// (rather than only an "=value" form baked into the one token), so that
// token must be skipped rather than mistaken for a positional create/rename
// argument.
var branchListValueFlags = map[string]bool{
	"--contains": true, "--no-contains": true, "--points-at": true, "--format": true, "--sort": true,
}

// isNCountFlag matches git tag's "-n"/"-n<num>" (how many message lines to
// show per tag) — a listing flag, but with an optional numeric suffix a
// fixed set can't enumerate.
func isNCountFlag(name string) bool {
	if name == "-n" {
		return true
	}
	if !strings.HasPrefix(name, "-n") {
		return false
	}
	for _, c := range name[2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(name) > 2
}

// branchOrTagMutates classifies `git branch`/`git tag` (sub names which one,
// rest is the args after it): read-only for any combination of listing/
// query flags (branchListFlags, plus tag's -n<num>), including a positional
// pattern argument once -l/--list has been seen (e.g.
// `git branch --list 'feat/*'`, `git tag -l 'v*'`) — mutating for a bare
// positional with no preceding -l/--list (create/rename) or any flag
// outside the listing set (delete, move, force-create, ...).
func branchOrTagMutates(sub string, rest []string) bool {
	sawList := false
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") {
			if sawList {
				continue // a pattern given to -l/--list, not a ref to create
			}
			return true
		}
		name := a
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			name = a[:eq]
		}
		if name == "-l" || name == "--list" {
			sawList = true
		}
		if sub == "tag" && isNCountFlag(name) {
			continue
		}
		if !branchListFlags[name] {
			return true
		}
		if branchListValueFlags[name] && !strings.Contains(a, "=") && i+1 < len(rest) {
			i++ // skip the value token
		}
	}
	return false
}

// gitMutates classifies a git invocation (args with "git" already stripped)
// as mutating or read-only. Unrecognized subcommands default to mutating.
func gitMutates(args []string) bool {
	if helpOnly(stripGlobal(args, gitValueOpts)) {
		return false
	}
	return gitArgsMutate(args)
}

// gitArgsMutate is gitMutates without the -h/--help exemption.
func gitArgsMutate(args []string) bool {
	args = stripGlobal(args, gitValueOpts)
	if len(args) == 0 {
		return false // bare `git` (or `git --version`) only prints
	}
	sub, rest := args[0], args[1:]
	if gitReadOnly[sub] {
		return false
	}
	first := ""
	if len(rest) > 0 {
		first = rest[0]
	}
	switch sub {
	case "branch", "tag":
		return branchOrTagMutates(sub, rest)
	case "stash":
		return !(first == "list" || first == "show")
	case "config":
		for _, a := range rest {
			if gitConfigReadFlags[a] {
				return false
			}
		}
		return !(first == "get" || first == "list")
	case "remote":
		// Leading -v/--verbose only makes the listing verbose; what
		// follows decides: nothing (a list) or show/get-url is a read,
		// anything else (`git remote -v add x y`) mutates.
		for len(rest) > 0 && (rest[0] == "-v" || rest[0] == "--verbose") {
			rest = rest[1:]
		}
		return !(len(rest) == 0 || rest[0] == "show" || rest[0] == "get-url")
	case "worktree":
		return first != "list"
	case "reflog":
		return !(first == "" || first == "show")
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
	"tag":       {"list": true, "l": true},
}

// jjMutates classifies a jj invocation (args with "jj" already stripped) as
// mutating or read-only. Unrecognized subcommands default to mutating.
func jjMutates(args []string) bool {
	if helpOnly(stripGlobal(args, jjValueOpts)) {
		return false
	}
	return jjArgsMutate(args)
}

// jjArgsMutate is jjMutates without the -h/--help exemption.
func jjArgsMutate(args []string) bool {
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
