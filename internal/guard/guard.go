// Package guard is a pure, no-I/O matcher over spec.GuardDecl: given the
// guards a workflow declares, it answers "is this command denied while
// these steps are active?" It has no side effects and touches no journal,
// filesystem or process — the (not-yet-built) enforcement hook is the
// package that will call it from a PreToolUse handler; see AGENTS.md's
// Status section and internal/spec's checkGuards doc comment for why
// guards: is validated but not yet enforced in this build.
package guard

import (
	"fmt"
	"regexp"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

// compiledGuard is one guards[] entry with its match: pre-compiled and its
// only_in: list indexed for O(1) membership checks.
type compiledGuard struct {
	decl   spec.GuardDecl
	re     *regexp.Regexp
	onlyIn map[string]bool
}

// Table is a compiled guard set, ready for repeated Denied calls without
// re-compiling every guard's match: regexp each time.
type Table struct {
	guards []compiledGuard
}

// continuationPattern matches a shell backslash-newline line continuation —
// a `\` immediately followed by `\n` or `\r\n`. This is deliberately a
// syntactic pattern, not a semantic one: POSIX sh also treats a backslash
// preceded by another backslash (`\\` + newline — a literal backslash, then
// a new command) as ending in an *escaped* backslash, not a line
// continuation, but this regexp cannot tell the two apart and still deletes
// that newline as if it were one. That is a known imprecision, not
// something this package tries to fix: guards match the canonical spelling
// of a command and nothing else (design/format-spec.md §10), and this is
// one more way, alongside `$()`, variable indirection and base64, that a
// guard's match: can be defeated by someone constructing the command text
// specifically to dodge it.
var continuationPattern = regexp.MustCompile(`\\\r?\n`)

// normalizeContinuations deletes every shell backslash-newline continuation
// in command entirely, matching what POSIX sh itself does with one: a line
// ending in an unescaped `\` has that backslash and the following newline
// removed outright, joining the next line directly onto the current one
// with nothing in between — not a space — so a command split across lines
// with a trailing `\` (the canonical way a long Bash tool call wraps)
// matches a guard's match: the same as its one-line spelling would —
// design/format-spec.md §10. Any whitespace already present next to the
// `\` or at the start of the following line survives the deletion as-is;
// only the backslash-newline pair itself is removed. This changes the
// command text Denied matches against, not the regexp engine's flags: `.`
// still does not match a literal `\n`, and `^`/`$` still anchor only at the
// ends of the whole string. A plain newline with nothing before it is left
// alone on purpose: two commands separated by a bare newline (no trailing
// `\`) still each get their own chance to match on their own line, since an
// unanchored match: finds a hit anywhere in the string regardless of
// embedded newlines — only `.` and the anchors treat `\n` specially, and
// this normalization doesn't change that.
func normalizeContinuations(command string) string {
	return continuationPattern.ReplaceAllString(command, "")
}

// Compile compiles every guard's match: as a Go regexp (RE2 syntax — close
// to POSIX ERE, no backreferences). It returns an error naming the first
// guard (in declaration order) whose match: fails to compile.
func Compile(guards []spec.GuardDecl) (*Table, error) {
	t := &Table{guards: make([]compiledGuard, 0, len(guards))}
	for _, g := range guards {
		re, err := regexp.Compile(g.Match)
		if err != nil {
			return nil, fmt.Errorf("guard %q: match %q does not compile: %w", g.ID, g.Match, err)
		}
		only := make(map[string]bool, len(g.OnlyIn))
		for _, s := range g.OnlyIn {
			only[s] = true
		}
		t.guards = append(t.guards, compiledGuard{decl: g, re: re, onlyIn: only})
	}
	return t, nil
}

// Denied reports the first guard (in declaration order) that denies
// command while activeSteps are active, or nil if none does. A guard
// denies command when its match: regexp matches anywhere in command
// (unanchored) and none of activeSteps is in its only_in: list — an empty
// or nil only_in: matches no step, so such a guard denies command in every
// step. activeSteps may be empty (no step is currently active) or carry
// several entries (a parallel step's branches are all active at once);
// either way, a guard denies unless at least one active step is
// permitted. Before matching, command has its shell backslash-newline line
// continuations deleted entirely (normalizeContinuations), joining the
// split lines the way a POSIX shell would; a plain newline with no
// preceding backslash is left alone.
//
// A nil *Table (an empty guard set) always returns nil: nothing to deny.
func (t *Table) Denied(activeSteps []string, command string) *spec.GuardDecl {
	if t == nil {
		return nil
	}
	command = normalizeContinuations(command)
	for _, cg := range t.guards {
		if !cg.re.MatchString(command) {
			continue
		}
		permitted := false
		for _, s := range activeSteps {
			if cg.onlyIn[s] {
				permitted = true
				break
			}
		}
		if permitted {
			continue
		}
		decl := cg.decl
		return &decl
	}
	return nil
}

// invalidGuardTableDecl is the synthetic guard Denied falls back to when
// Compile failed but re-scanning guards for the offending entry (below)
// somehow found none. That branch should be unreachable — Compile's own
// error comes from exactly the same regexp.Compile call this scan repeats
// — but "should be unreachable" is not a license to return nil (= allowed)
// from a function whose whole contract is failing closed; a broken guard
// table must deny, not silently pass everything through. Its ID is
// deliberately outside checkGuards' accepted id: shape (internal/spec's
// stepIDPattern forbids "/"), so a caller can tell this apart from any
// guard an author could actually have declared.
var invalidGuardTableDecl = spec.GuardDecl{ID: "invalid/guard-table"}

// Denied is the one-shot form of Compile followed by Table.Denied, for a
// caller that does not need to reuse the compiled table across calls. On a
// compile error it fails closed: rather than allow command through because
// one guard's match: is broken, it returns the first guard (in declaration
// order) whose match: fails to compile — a broken guard blocks the same
// way a matched one would, since "the guard table could not be built" is
// not a safe reason to allow anything. In the should-be-unreachable case
// where that guard cannot be found again, it still returns a non-nil
// synthetic denying guard (invalidGuardTableDecl) rather than nil, so the
// fail-closed contract holds even then.
func Denied(guards []spec.GuardDecl, activeSteps []string, command string) *spec.GuardDecl {
	t, err := Compile(guards)
	if err != nil {
		return firstCompileFailure(guards)
	}
	return t.Denied(activeSteps, command)
}

// firstCompileFailure returns the first guard (in declaration order) whose
// match: fails to compile, or — if none does, which Denied only calls this
// in a branch where it should be unreachable — a non-nil synthetic denying
// guard (invalidGuardTableDecl). Split out from Denied so the
// should-be-unreachable fallback is exercisable directly by a test rather
// than only by a Compile error this package cannot otherwise construct
// without a compile-broken guard already in the slice.
func firstCompileFailure(guards []spec.GuardDecl) *spec.GuardDecl {
	for i := range guards {
		if _, err := regexp.Compile(guards[i].Match); err != nil {
			decl := guards[i]
			return &decl
		}
	}
	decl := invalidGuardTableDecl
	return &decl
}

// Normalize returns a copy of guards with every nil OnlyIn replaced by a
// non-nil empty slice, so that encoding/json marshals only_in: as [] rather
// than null. Plain json.Marshal of a []spec.GuardDecl with a nil OnlyIn
// writes "only_in":null (proven by TestGuardsJSON_RoundTrip in this
// package's test file) — the guards.json contract another worker's
// enforcement hook reads expects only_in to always be an array, so any
// writer of that file must call Normalize first. Normalize does not mutate
// its input.
func Normalize(guards []spec.GuardDecl) []spec.GuardDecl {
	out := make([]spec.GuardDecl, len(guards))
	for i, g := range guards {
		out[i] = g
		if out[i].OnlyIn == nil {
			out[i].OnlyIn = []string{}
		}
	}
	return out
}
