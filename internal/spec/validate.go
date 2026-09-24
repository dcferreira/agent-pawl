package spec

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"time"

	"github.com/dcferreira/agent-pawl/internal/render"
)

// Report is the result of Validate: the ordered list of error messages, and
// the soft: census that design/format-spec.md §H asks be printed every time.
type Report struct {
	// Errors is empty when the workflow validates clean.
	Errors []string

	TotalSteps  int
	SoftCount   int
	SoftStepIDs []string
}

// SoftPercent is the percentage of steps that advance on a soft
// postcondition.
func (r *Report) SoftPercent() float64 {
	if r.TotalSteps == 0 {
		return 0
	}
	return 100 * float64(r.SoftCount) / float64(r.TotalSteps)
}

// Validate runs the static checks against w and returns a Report. The
// checks implemented are design/format-spec.md §H rules 1, 2, 3, 3b, 4, 5,
// 6, 7, 8, 9, 9b, 10, 11, 12, 13, 14, 15, 18 (Ruling R4), plus guards:
// validation (checkGuards — id required+unique, match: required and
// RE2-compilable, only_in: required with rule 15 checking each entry names
// a declared step), the R8 invariants: rejection (guards: is no longer
// rejected outright — see checkGuards's own doc comment), and the kind:
// parallel branches: validation (checkParallelBranches, rule 17).
// deterministic, agentic, wait, human and parallel are all supported
// (Ruling R3); wait's own required fields (poll:) and duration parsing
// (every:, timeout:) are checked alongside rule 7, and human's own required
// fields and options routing are checked by rule 8. Rule 16 and the two
// warnings are deferred per R4.
func Validate(w *Workflow) (*Report, error) {
	if w == nil {
		return nil, fmt.Errorf("spec: Validate: nil workflow")
	}
	// Defensive: a *Workflow built any other way than Load (a test literal,
	// or a future journal round-trip) has its defaulted int fields at zero.
	// applyDefaults is idempotent, so re-running it here is always safe,
	// even when Load already ran (Finding F8).
	applyDefaults(w)

	var errs []string
	checkRequiredFields(w, &errs)
	checkUnknownFields(w, &errs)
	checkGuards(w, &errs)
	checkInvariants(w, &errs)
	checkRetry(w, &errs)
	checkKindSupport(w, &errs)
	checkParallelBranches(w, &errs)
	checkDuplicateStepIDs(w, &errs)
	checkStepIDFormat(w, &errs)
	checkNextOutcomesExclusive(w, &errs)
	checkKindRequiredFields(w, &errs)
	checkAgenticWrites(w, &errs)

	startOK := checkStartExists(w, &errs)

	checkRule1(w, &errs)
	if startOK {
		checkRule2(w, &errs)
	}
	checkRule3(w, &errs)
	checkRule3b(w, &errs)
	checkRule4(w, &errs)
	checkRule5(w, &errs)
	checkRule6(w, &errs)
	checkRule7(w, &errs)
	checkWaitDurations(w, &errs)
	checkRule8(w, &errs)
	checkRule8Multi(w, &errs)
	checkRule9(w, &errs)
	checkRule9b(w, &errs)
	checkRule10(w, &errs)
	checkRule11(w, &errs)
	checkRule12(w, &errs)
	checkRule13(w, &errs)
	checkRule14(w, &errs)
	checkRule18(w, &errs)

	report := &Report{TotalSteps: len(w.Steps)}
	for _, s := range w.Steps {
		if s.Postcondition != nil && s.Postcondition.Soft {
			report.SoftCount++
			report.SoftStepIDs = append(report.SoftStepIDs, s.ID)
		}
	}
	report.Errors = errs
	return report, nil
}

func fileErr(w *Workflow, detail string) string {
	return fmt.Sprintf("%s: %s", w.Path, detail)
}

func stepErr(w *Workflow, stepID, detail string) string {
	return fmt.Sprintf("%s: step %q: %s", w.Path, stepID, detail)
}

func terminalErr(w *Workflow, terminalID, detail string) string {
	return fmt.Sprintf("%s: terminal %q: %s", w.Path, terminalID, detail)
}

func sortedOutcomeKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checkInvariants rejects invariants: (Ruling R8): it is still deferred,
// and silently disabling enforcement is the failure class DESIGN.md §5
// exists to prevent, so a declared block is rejected outright rather than
// ignored. guards: used to be rejected the same way; it no longer is — see
// checkGuards.
func checkInvariants(w *Workflow, errs *[]string) {
	if len(w.Invariants) > 0 {
		*errs = append(*errs, fileErr(w, "invariants: is not implemented in this build; remove the invariants: block"))
	}
}

// guardRef names a guards[] entry for an error message: its quoted id when
// one was declared, else its positional index (guards[i]) — used for
// exactly the fields (match:, only_in:) that still need reporting even when
// id: itself is missing or invalid, so one bad guard produces one error per
// bad field rather than being skipped wholesale.
func guardRef(g GuardDecl, i int) string {
	if g.ID != "" {
		return fmt.Sprintf("guard %q", g.ID)
	}
	return fmt.Sprintf("guards[%d]", i)
}

// checkGuards validates guards: (design/format-spec.md §B.10, §D, §H rule
// 15). Unlike invariants:, guards: is no longer rejected outright: it is
// accepted and validated here so the enforcement hook (internal/hook,
// internal/guard) has something to consume — the hook reads a live run's
// guards straight out of plan.json's Workflow.Guards, not a separate
// guards.json. Accepted-and-validated is only advisory enforcement, though:
// it's a PreToolUse pattern match against the Bash command, not a semantic
// guarantee ($(), a variable, or a renamed binary all evade it), and it's
// only live at all while the hooks are actually installed and firing —
// internal/cli's run banner prints "guards: N advisory (pattern-matched)"
// when a fresh heartbeat exists, or "guards: N declared, NOT enforced" when
// enforcement is off, whenever N > 0 (nothing extra when N == 0).
//
// Per guards[] entry:
//   - id: is required and must be unique across the file (reusing
//     stepIDPattern — a guard id ends up as a JSON key downstream, so the
//     same identifier-safety reasoning as a step id applies).
//   - match: is required and must compile as a Go regexp (RE2 syntax —
//     close to POSIX ERE, no backreferences); it is matched unanchored
//     against the whole command string at enforcement time (internal/guard).
//     It must also not be able to match zero characters: since matching is
//     unanchored, a pattern like `a*`, `x?`, `foo|`, or one that can only
//     ever produce a zero-width match such as `\b` or `git push|\b`,
//     matches every command, which would make the guard deny (or, with a
//     non-empty only_in:, effectively deny outside its listed steps)
//     unconditionally — almost certainly a typo, not an intended "block
//     everything" guard. Probing with re.MatchString("") alone would miss
//     `\b`: it finds no boundary in the empty string, yet still matches
//     (zero-width) inside any command containing a word character, since
//     matching is unanchored. So the check instead parses match: with
//     regexp/syntax (the same syntax.Perl flags regexp.Compile itself
//     uses), Simplifies the tree, and walks it (minMatchWidth, below) to
//     compute the minimum number of runes any match can consume, rejecting
//     when that minimum is 0. This check is deliberately narrow: a match:
//     that can never match anything at all (impossibleWidth, below — e.g.
//     a character class that excludes every rune) is a dead guard, not a
//     zero-width one, and is not rejected here; it is a different mistake
//     this validator does not currently catch.
//   - only_in: is a required key (a project ruling, not implied by §D's
//     table): a missing, null (only_in: ~ / only_in: null), or bare
//     (only_in: with nothing after the colon) only_in: is far more likely
//     to be an author who forgot it than one who means "never active", so
//     the validator makes that distinguishable from meaning it — only_in:
//     [] is the explicit way to deny a guard everywhere. yaml.v3 already
//     decodes all three of missing/null/bare the same way, as a nil slice,
//     and an empty list as a distinct non-nil empty slice, so
//     GuardDecl.OnlyIn needs no change to tell them apart; the error
//     message says "required and must be a list" rather than "missing"
//     so it reads correctly even when the author's line visibly has
//     only_in: on it.
//
// Unknown fields inside a guards[] entry are rejected earlier, at Load
// time, by the decoder's KnownFields(true) (internal/spec/load.go) — guards:
// has no custom UnmarshalYAML the way Step does, so it goes through the
// decoder's own field-name checking rather than checkUnknownFields.
func checkGuards(w *Workflow, errs *[]string) {
	seen := map[string]bool{}
	for i, g := range w.Guards {
		ref := guardRef(g, i)

		if g.ID == "" {
			*errs = append(*errs, fileErr(w, fmt.Sprintf("%s: id: is required; add a unique id", ref)))
		} else {
			if !stepIDPattern.MatchString(g.ID) {
				*errs = append(*errs, fileErr(w, fmt.Sprintf(
					"guard %q: id is not a valid guard id; use only letters, digits, \"_\" and \"-\", starting with a letter or digit — rename the guard", g.ID)))
			}
			if seen[g.ID] {
				*errs = append(*errs, fileErr(w, fmt.Sprintf(
					"guard %q is declared more than once; guard ids must be unique — rename one of them", g.ID)))
			}
			seen[g.ID] = true
		}

		if g.Match == "" {
			*errs = append(*errs, fileErr(w, fmt.Sprintf("%s: match: is required; add a match: regexp", ref)))
		} else if _, err := regexp.Compile(g.Match); err != nil {
			*errs = append(*errs, fileErr(w, fmt.Sprintf(
				"%s: match: %q does not compile as a regexp: %s", ref, g.Match, err)))
		} else if syn, perr := syntax.Parse(g.Match, syntax.Perl); perr == nil && minMatchWidth(syn.Simplify()) == 0 {
			// perr is deliberately swallowed rather than reported as its own
			// error: regexp.Compile above already proved g.Match compiles,
			// and Compile itself parses with these same syntax.Perl flags,
			// so a syntax.Parse failure here would mean the two parsers
			// disagree — not something an author did wrong. Skipping the
			// width check rather than rejecting is the fail-open choice for
			// an internal inconsistency, not for anything an author wrote.
			*errs = append(*errs, fileErr(w, fmt.Sprintf(
				"%s: match: %q can match zero characters; a guard's match: must require at least one character", ref, g.Match)))
		}

		if g.OnlyIn == nil {
			*errs = append(*errs, fileErr(w, fmt.Sprintf(
				"%s: only_in: is required and must be a list; use only_in: [] to deny it in every step", ref)))
			continue
		}
		for _, stepID := range g.OnlyIn {
			if w.StepByID(stepID) == nil {
				*errs = append(*errs, fileErr(w, fmt.Sprintf(
					"%s: rule 15: only_in: %q does not name a declared step; declare step %q or fix the typo", ref, stepID, stepID)))
			}
		}
	}
}

// impossibleWidth is the sentinel minMatchWidth returns for a subexpression
// that regexp/syntax says can never match anything at all (OpNoMatch) —
// deliberately not 0. A guard whose match: matches nothing is a different,
// separate problem from one whose match: matches zero characters; giving
// OpNoMatch a width of 0 would make an enclosing OpConcat or OpAlternate
// look like it can match empty when in fact it can never match anything,
// which is the opposite kind of guard mistake. It is large enough to
// survive OpConcat's summing and OpRepeat's multiplication without ever
// looking like a genuine small width, and small enough to never overflow a
// plain int doing that arithmetic on any regexp this validator will see.
const impossibleWidth = 1 << 30

// minMatchWidth returns the minimum number of runes any match of re can
// consume, by recursively walking the regexp/syntax parse tree (re must
// already have been through Simplify, per checkGuards) — see checkGuards's
// doc comment for why re.MatchString("") alone is not enough:
//
//   - OpLiteral: the literal's rune count.
//   - OpCharClass: 1, unless the class has zero ranges (re.Rune is empty),
//     in which case impossibleWidth — see the case's own comment below.
//   - OpAnyCharNotNL, OpAnyChar: 1 — "any character" always consumes
//     exactly one rune.
//   - OpBeginLine, OpEndLine, OpBeginText, OpEndText, OpWordBoundary,
//     OpNoWordBoundary, OpEmptyMatch: 0 — these are all empty-width
//     assertions or the empty match itself; none of them consumes a rune.
//   - OpNoMatch: impossibleWidth (above) — a subexpression that can never
//     match at all is not the same failure as one that matches zero
//     characters. In practice regexp/syntax's parser only ever produces
//     OpNoMatch itself from an empty alternate, which is not reachable by
//     parsing ordinary author-supplied syntax (Perl-flag parsing rejects
//     the constructs, like a min>max repeat, that Simplify would otherwise
//     fold into OpNoMatch) — the OpCharClass case above is the practical
//     way an impossible-to-match subexpression actually shows up here.
//   - OpCapture: its single subexpression's width, unchanged.
//   - OpStar, OpQuest: 0 — both permit zero repetitions.
//   - OpPlus: its single subexpression's width — one repetition is
//     mandatory.
//   - OpRepeat: Min times its single subexpression's width.
//   - OpConcat: the sum of every subexpression's width.
//   - OpAlternate: the minimum width across its branches — a match only
//     has to take the cheapest one.
func minMatchWidth(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune)
	case syntax.OpCharClass:
		// A character class can end up with zero ranges — regexp/syntax
		// does not fold this into OpNoMatch the way it does an empty
		// alternate or a degenerate repeat (see the OpNoMatch case below):
		// e.g. `[^\x00-\x{10FFFF}]`, whose negation excludes every valid
		// rune, parses and Simplifies to an OpCharClass with re.Rune ==
		// nil, not OpNoMatch. Left at the default width of 1, it would
		// look exactly like a normal one-rune class to OpConcat/OpAlternate,
		// even though it can never match anything — the same
		// "impossible, not merely zero-width" case OpNoMatch exists to
		// flag. So it gets the same sentinel.
		if len(re.Rune) == 0 {
			return impossibleWidth
		}
		return 1
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return 1
	case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpEmptyMatch:
		return 0
	case syntax.OpNoMatch:
		return impossibleWidth
	case syntax.OpCapture:
		return minMatchWidth(re.Sub[0])
	case syntax.OpStar, syntax.OpQuest:
		return 0
	case syntax.OpPlus:
		return minMatchWidth(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min * minMatchWidth(re.Sub[0])
	case syntax.OpConcat:
		total := 0
		for _, sub := range re.Sub {
			total += minMatchWidth(sub)
		}
		return total
	case syntax.OpAlternate:
		min := impossibleWidth
		for _, sub := range re.Sub {
			if width := minMatchWidth(sub); width < min {
				min = width
			}
		}
		return min
	default:
		return 0
	}
}

// checkRetry rejects retry: (Ruling R7): parsed, never silently ignored.
func checkRetry(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Retry != nil {
			*errs = append(*errs, stepErr(w, s.ID, "retry: is not implemented in this build; remove the retry: block"))
		}
	}
}

// checkKindSupport rejects kinds this build does not execute (Ruling R3).
// deterministic, agentic, wait, human and parallel are all implemented now
// (wait's own validation — poll:, timeout:, duration parsing,
// outcome-routing completeness — and human's own validation — §H rules 7
// and 8 — both landed); only unknown kinds are rejected here as a typo.
func checkKindSupport(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		switch s.Kind {
		case "deterministic", "agentic", "wait", "human", "parallel":
			// supported
		default:
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"kind %q is not one of deterministic, agentic, wait, human, parallel; fix the typo", s.Kind)))
		}
	}
}

// checkRequiredFields checks the file-level presence requirements of §D:
// workflow:, start: and a non-empty steps: list. §H has no rule number for
// these, but §D marks them required, and an empty or garbage file validating
// clean is the worst possible first-run experience (Finding F1).
func checkRequiredFields(w *Workflow, errs *[]string) {
	if w.Workflow == "" {
		*errs = append(*errs, fileErr(w, "workflow: is required; add workflow: <name>"))
	}
	if w.Start == "" {
		*errs = append(*errs, fileErr(w, "start: is required; add start: <step-id>"))
	}
	if len(w.Steps) == 0 {
		*errs = append(*errs, fileErr(w, "steps: must declare at least one step; add one"))
	}
}

// checkStartExists checks that start: resolves to a declared step id
// (Finding F2). It reports nothing when start: is empty (checkRequiredFields
// already covers that) and returns false whenever start: cannot anchor a
// reachability walk, so callers can skip checkRule2's cascade rather than
// reporting every step as unreachable from a start: that does not exist.
func checkStartExists(w *Workflow, errs *[]string) bool {
	if w.Start == "" {
		return false
	}
	if w.StepByID(w.Start) == nil {
		*errs = append(*errs, fileErr(w, fmt.Sprintf(
			"start: %q does not name a declared step; declare step %q or fix the typo", w.Start, w.Start)))
		return false
	}
	return true
}

// checkUnknownFields reports a step-level field this build does not
// recognise, with a spelling suggestion when one is close (Finding F6).
func checkUnknownFields(w *Workflow, errs *[]string) {
	known := make([]string, 0, len(stepKnownFields))
	for k := range stepKnownFields {
		known = append(known, k)
	}
	sort.Strings(known)

	for _, s := range w.Steps {
		for _, field := range s.UnknownFields {
			if suggestion := closestField(field, known); suggestion != "" {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"has unknown field %q (did you mean %q?); rename it or remove it", field, suggestion)))
			} else {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"has unknown field %q; remove it or check the spelling against design/format-spec.md §D", field)))
			}
		}
	}
}

// closestField returns the candidate closest to field by edit distance, if
// any is within 2 edits, else "".
func closestField(field string, candidates []string) string {
	best := ""
	bestDist := 3 // anything further than 2 edits is not a useful suggestion
	for _, c := range candidates {
		if d := levenshtein(field, c); d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

// checkDuplicateStepIDs implements §D's "Unique step name" requirement
// (Finding F5a). §H has no rule number for it, but a duplicate id leaves the
// second declaration silently dead in the graph.
func checkDuplicateStepIDs(w *Workflow, errs *[]string) {
	seen := map[string]bool{}
	for _, s := range w.Steps {
		if s.ID == "" {
			continue
		}
		if seen[s.ID] {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"id %q is declared more than once; step ids must be unique — rename one of them", s.ID)))
		}
		seen[s.ID] = true
	}
}

// stepIDPattern is an identifier-like format for step id: no §H rule number
// covers this (no field in §D is documented as free-form-yet-unsafe), but
// a step id ends up as a filename component in the engine's per-attempt log
// files (internal/engine's writeStepOutput), so an id containing a path
// separator or a "." segment must be rejected here rather than discovered
// only when it escapes a directory (finding N1). Letters, digits,
// underscore and hyphen, starting with a letter or digit — no ".", "/", or
// leading/trailing "-".
var stepIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// checkStepIDFormat implements finding N1's validator half: it rejects a
// step id that isn't identifier-like, so an id like "../../escaped" is
// refused at author time rather than reaching the engine, which sanitises
// defensively but should never see one from a workflow that passed
// Validate.
func checkStepIDFormat(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.ID == "" || stepIDPattern.MatchString(s.ID) {
			continue
		}
		*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
			"id %q is not a valid step id; use only letters, digits, \"_\" and \"-\", starting with a letter or digit — rename the step", s.ID)))
	}
}

// checkNextOutcomesExclusive implements §D's "Mutually exclusive" / §B.11's
// "exactly one of next:/outcomes:" requirement (Finding F5b).
func checkNextOutcomesExclusive(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Next != "" && len(s.Outcomes) > 0 {
			*errs = append(*errs, stepErr(w, s.ID, "has both next: and outcomes:; these are mutually exclusive — keep only one"))
		}
	}
}

// checkKindRequiredFields implements §D's per-kind required fields that §H
// has no rule number for (Finding F5c): run: on deterministic, description:
// on agentic.
func checkKindRequiredFields(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		switch s.Kind {
		case "deterministic":
			if s.Run == "" {
				*errs = append(*errs, stepErr(w, s.ID, "kind: deterministic requires run:; add a run: command"))
			}
		case "agentic":
			if s.Description == "" {
				*errs = append(*errs, stepErr(w, s.ID, "kind: agentic requires description:; add a description: of the intent, constraints and definition of done"))
			}
		case "wait":
			if s.Poll == "" {
				*errs = append(*errs, stepErr(w, s.ID, "kind: wait requires poll:; add a poll: command"))
			}
		}
	}
}

// checkAgenticWrites implements the controller ruling on Finding F7: §D
// requires a typed writes: map on agentic (it is the subagent's return
// schema), and §B.6 rests one of the engine's three load-bearing guarantees
// on it, so an agentic step with no writes:, an untyped list writes:, or a
// typed map that declares no types is rejected the same way R8 rejects
// guards: — a schema that looks present but is not is the same failure
// class as one that is silently disabled.
func checkAgenticWrites(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Kind != "agentic" {
			continue
		}
		missing := !s.Writes.IsTyped || len(s.Writes.Keys) == 0
		if !missing {
			for _, k := range s.Writes.Keys {
				if s.Writes.Types[k] == "" {
					missing = true
					break
				}
			}
		}
		if missing {
			*errs = append(*errs, stepErr(w, s.ID,
				"kind: agentic requires a typed writes: map — it is the subagent's return schema; add writes: {<key>: {type: string|integer|json}} declaring each key the subagent returns"))
		}
	}
}

// checkRule1 implements §H rule 1.
func checkRule1(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Next != "" && !w.IsValidTarget(s.Next) {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 1: next: %q names a step or terminal that does not exist; declare step %q, add it under terminal:, or fix the typo", s.Next, s.Next)))
		}
		for _, outcome := range sortedOutcomeKeys(s.Outcomes) {
			target := s.Outcomes[outcome]
			if !w.IsValidTarget(target) {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 1: outcomes[%s]: %q names a step or terminal that does not exist; declare step %q, add it under terminal:, or fix the typo", outcome, target, target)))
			}
		}
		for _, c := range s.Catch {
			if !w.IsValidTarget(c.Next) {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 1: catch[on=%s].next: %q names a step or terminal that does not exist; declare step %q, add it under terminal:, or fix the typo", c.On, c.Next, c.Next)))
			}
		}
	}
}

func edgesOf(s *Step) []string {
	var out []string
	if s.Next != "" {
		out = append(out, s.Next)
	}
	for _, t := range s.Outcomes {
		out = append(out, t)
	}
	for _, c := range s.Catch {
		out = append(out, c.Next)
	}
	if s.Kind == "parallel" {
		out = append(out, s.Branches...)
	}
	return out
}

// branchStepIDs maps every step id used as a parallel step's branch to the
// id of the (first-declared, in w.Steps order) parallel step that claims it.
// checkParallelBranches uses it to catch a step id claimed as a branch by
// more than one parallel step, and checkRule3 uses it to identify branch
// steps so that rule can skip them: a branch step is required (by
// checkParallelBranches) to declare no next:/outcomes: of its own, since the
// owning parallel step is the sole owner of routing for the whole group.
func branchStepIDs(w *Workflow) map[string]string {
	owner := map[string]string{}
	for _, s := range w.Steps {
		if s.Kind != "parallel" {
			continue
		}
		for _, b := range s.Branches {
			if _, claimed := owner[b]; !claimed {
				owner[b] = s.ID
			}
		}
	}
	return owner
}

// checkParallelBranches implements the validation rules for kind: parallel's
// branches: (design/format-spec.md §C, §D): branches: is required with at
// least 2 entries, each entry must resolve to a declared deterministic or
// agentic step (no nesting), no duplicates within one list, no step id
// claimed as a branch by more than one parallel step, a branch may not be
// the workflow's start: step, and a branch step may declare none of the
// fields the owning parallel step alone controls (next:, outcomes:, catch:,
// attempts:, attempt_key:, max_visits:).
func checkParallelBranches(w *Workflow, errs *[]string) {
	owner := branchStepIDs(w)

	forbidden := []struct {
		field string
		has   func(Step) bool
	}{
		{"next:", func(s Step) bool { return s.Next != "" }},
		{"outcomes:", func(s Step) bool { return len(s.Outcomes) > 0 }},
		{"catch:", func(s Step) bool { return len(s.Catch) > 0 }},
		{"attempts:", func(s Step) bool { return s.AttemptsRaw != nil }},
		{"attempt_key:", func(s Step) bool { return s.AttemptKey != "" }},
		{"max_visits:", func(s Step) bool { return s.MaxVisitsRaw != nil }},
	}

	for _, s := range w.Steps {
		if s.Kind != "parallel" {
			continue
		}
		if len(s.Branches) < 2 {
			*errs = append(*errs, stepErr(w, s.ID,
				"kind: parallel requires branches: with at least 2 entries"))
			continue
		}

		seen := map[string]bool{}
		for i, b := range s.Branches {
			branch := w.StepByID(b)
			switch {
			case branch == nil:
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"branches[%d]: %q does not name a declared step", i, b)))
			case branch.Kind != "deterministic" && branch.Kind != "agentic":
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"branches[%d]: step %q has kind %q, but a parallel branch must be deterministic or agentic (no nesting)", i, b, branch.Kind)))
			}

			if seen[b] {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"branches[%d]: step %q is listed more than once", i, b)))
			}
			seen[b] = true

			if first := owner[b]; first != "" && first != s.ID {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"branches[%d]: step %q is already claimed as a branch by parallel step %q; a step may be a branch of only one parallel step", i, b, first)))
			}

			if b == w.Start {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"branches[%d]: step %q is start:, but a parallel branch may not be the workflow's start step", i, b)))
			}

			if branch != nil {
				for _, f := range forbidden {
					if f.has(*branch) {
						*errs = append(*errs, stepErr(w, b, fmt.Sprintf(
							"declares %s, but it is a branch of parallel step %q, which owns routing and retry for the whole group; remove %s", f.field, s.ID, f.field)))
					}
				}
			}
		}
	}
}

// checkRule2 implements §H rule 2. Terminal ids (declared or the implicit
// done/blocked) are treated as graph nodes per Ruling R12, but only Steps
// are ever reported unreachable.
func checkRule2(w *Workflow, errs *[]string) {
	visited := map[string]bool{}
	if w.Start != "" {
		stack := []string{w.Start}
		visited[w.Start] = true
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			step := w.StepByID(id)
			if step == nil {
				continue // terminal: a graph node, but a leaf (§B.12).
			}
			for _, t := range edgesOf(step) {
				if !visited[t] {
					visited[t] = true
					stack = append(stack, t)
				}
			}
		}
	}
	for _, s := range w.Steps {
		if !visited[s.ID] {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 2: is unreachable from start: %q; add an edge to it or remove it", w.Start)))
		}
	}
}

// checkRule3 implements §H rule 3. A parallel branch step is exempt: it is
// required (checkParallelBranches) to declare no next:/outcomes: of its own,
// since the owning parallel step is the sole owner of routing for the whole
// group.
func checkRule3(w *Workflow, errs *[]string) {
	branches := branchStepIDs(w)
	for _, s := range w.Steps {
		if _, isBranch := branches[s.ID]; isBranch {
			continue
		}
		if s.Next == "" && len(s.Outcomes) == 0 {
			*errs = append(*errs, stepErr(w, s.ID, "rule 3: has no next: or outcomes:; add one naming its successor step or a terminal"))
		}
	}
}

// requiredOutcomesFor names the outcomes a step's kind must always route in
// addition to its author-named tokens (§B.11). Only wait and human carry a
// mandatory extra route (timeout) in this build; deterministic's producible
// set is exactly its author-named tokens, and agentic never has outcomes:
// at all (rule 9).
func requiredOutcomesFor(s Step) []string {
	switch s.Kind {
	case "wait", "human":
		return []string{"timeout"}
	default:
		return nil
	}
}

// checkRule3b implements §H rule 3b.
func checkRule3b(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if len(s.Outcomes) == 0 {
			continue
		}
		for _, req := range requiredOutcomesFor(s) {
			if _, ok := s.Outcomes[req]; !ok {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 3b: has no route for outcome %q; add outcomes: {%s: <step-or-terminal>, ...}", req, req)))
			}
		}
	}
}

// checkRule4 implements §H rule 4.
func checkRule4(w *Workflow, errs *[]string) {
	declared := map[string]bool{}
	for k := range w.State {
		declared[k] = true
	}
	for k := range w.Args {
		declared[k] = true
	}

	scanTemplate := func(loc, tmpl string, add func(string)) {
		if tmpl == "" {
			return
		}
		for _, key := range render.Keys(tmpl) {
			if declared[key] || isPseudoKey(key) {
				continue
			}
			add(fmt.Sprintf(
				"rule 4: %s uses ${%s}, which is not declared in state:/args: and is not an engine pseudo-key; declare %q under state: or args:, or fix the typo",
				loc, key, key))
		}
	}
	noSubst := func(loc, value string, add func(string)) {
		// render.Keys is escape-aware ("$${" renders a literal "${" and is
		// not a substitution attempt); a plain substring check on "${"
		// false-positives on that escape (Finding F10).
		if len(render.Keys(value)) > 0 {
			add(fmt.Sprintf("rule 4: %s does not support ${...} substitution; remove it (%q)", loc, value))
		}
	}

	for _, s := range w.Steps {
		add := func(msg string) { *errs = append(*errs, stepErr(w, s.ID, msg)) }

		noSubst("kind", s.Kind, add)
		if s.Next != "" {
			noSubst("next", s.Next, add)
		}
		for _, outcome := range sortedOutcomeKeys(s.Outcomes) {
			noSubst(fmt.Sprintf("outcomes key %q", outcome), outcome, add)
			noSubst(fmt.Sprintf("outcomes[%s]", outcome), s.Outcomes[outcome], add)
		}
		for _, c := range s.Catch {
			noSubst("catch[].next", c.Next, add)
		}

		scanTemplate("run", s.Run, add)
		scanTemplate("poll", s.Poll, add)
		scanTemplate("description", s.Description, add)
		scanTemplate("question", s.Question, add)
		scanTemplate("attempt_key", s.AttemptKey, add)
		for i, c := range s.Context {
			scanTemplate(fmt.Sprintf("context[%d]", i), c.Value, add)
		}
		if s.Postcondition != nil {
			scanTemplate("postcondition.command", s.Postcondition.Command, add)
			equalsKeys := make([]string, 0, len(s.Postcondition.Equals))
			for k := range s.Postcondition.Equals {
				equalsKeys = append(equalsKeys, k)
			}
			sort.Strings(equalsKeys)
			for _, k := range equalsKeys {
				if sv, ok := s.Postcondition.Equals[k].(string); ok {
					scanTemplate(fmt.Sprintf("postcondition.equals[%s]", k), sv, add)
				}
			}
		}
	}

	terminalIDs := make([]string, 0, len(w.Terminal))
	for id := range w.Terminal {
		terminalIDs = append(terminalIDs, id)
	}
	sort.Strings(terminalIDs)
	for _, id := range terminalIDs {
		t := w.Terminal[id]
		add := func(msg string) { *errs = append(*errs, terminalErr(w, id, msg)) }
		scanTemplate("message", t.Message, add)
	}
}

// checkRule5 implements §H rule 5.
func checkRule5(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		for _, k := range s.Writes.Keys {
			if _, ok := w.Args[k]; ok {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 5: writes: %q, but %q is declared under args: and args are read-only; remove it from writes: or declare it under state: instead", k, k)))
			}
		}
	}
}

// checkRule6 implements §H rule 6.
func checkRule6(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Kind == "agentic" && s.Postcondition == nil {
			*errs = append(*errs, stepErr(w, s.ID, "rule 6: kind: agentic requires postcondition: (soft: true is not an exemption from declaring one); add a postcondition:"))
		}
	}
}

// checkRule7 implements the first half of §H rule 7: a wait or human step
// with no timeout:. (The second half — no route for the timeout outcome —
// is covered by rule 3b via requiredOutcomesFor, since a wait/human step's
// producible token universe is exactly its outcomes: map's own keys plus
// the reserved timeout token, mirrored from how deterministic's own
// author-named tokens are just its outcomes: map's keys.)
func checkRule7(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Kind != "wait" && s.Kind != "human" {
			continue
		}
		if s.Timeout == "" {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 7: kind: %s requires timeout:; add a timeout: duration (e.g. \"5m\", \"1h\")", s.Kind)))
		}
	}
}

// checkWaitDurations validates that a wait step's every:/timeout: parse as
// Go durations (§D's "duration" type column). No §H rule number covers a
// field's type shape directly — like rule 10's emits: enum check, this is a
// type check the field table implies but §H does not enumerate by number —
// so it is reported without a "rule N:" prefix, the same convention
// checkKindRequiredFields uses for its own un-numbered findings.
func checkWaitDurations(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Kind != "wait" {
			continue
		}
		if s.Every != "" {
			if _, err := time.ParseDuration(s.Every); err != nil {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"every: %q is not a valid duration; use Go duration syntax, e.g. \"60s\", \"5m\"", s.Every)))
			}
		}
		if s.Timeout != "" {
			if _, err := time.ParseDuration(s.Timeout); err != nil {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"timeout: %q is not a valid duration; use Go duration syntax, e.g. \"5m\", \"1h\"", s.Timeout)))
			}
		}
	}
}

// checkRule8 implements §H rule 8's human-scoped sub-conditions (§B.5):
// exactly one of options:/options_from:; every static option: routed in
// outcomes: whenever outcomes: is used at all; and writes: with exactly one
// key required whenever a chosen: route, options_from:, or multi: true is
// in play. The multi:-on-a-non-human-kind sub-condition is not scoped to
// human — see checkRule8Multi.
func checkRule8(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Kind != "human" {
			continue
		}

		hasOptions := len(s.Options) > 0
		hasOptionsFrom := s.OptionsFrom != ""
		switch {
		case hasOptions && hasOptionsFrom:
			*errs = append(*errs, stepErr(w, s.ID,
				"rule 8: kind: human sets both options: and options_from:; keep only one"))
		case !hasOptions && !hasOptionsFrom:
			*errs = append(*errs, stepErr(w, s.ID,
				"rule 8: kind: human requires exactly one of options: or options_from:; add one"))
		}

		if hasOptions && !hasOptionsFrom && len(s.Outcomes) > 0 {
			for _, opt := range s.Options {
				if _, ok := s.Outcomes[opt]; !ok {
					*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
						"rule 8: options: %q has no route in outcomes:; add outcomes: {%s: <step-or-terminal>, ...}", opt, opt)))
				}
			}
		}

		var reasons []string
		if _, ok := s.Outcomes["chosen"]; ok {
			reasons = append(reasons, "outcomes: {chosen: ...}")
		}
		if hasOptionsFrom {
			reasons = append(reasons, "options_from:")
		}
		if s.Multi {
			reasons = append(reasons, "multi: true")
		}
		if len(reasons) > 0 && len(s.Writes.Keys) != 1 {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 8: %s requires writes: with exactly one key (found %d); add writes: [<key>] naming the key that receives the answer",
				strings.Join(reasons, " and "), len(s.Writes.Keys))))
		}
	}
}

// checkRule8Multi implements §H rule 8's "multi: on any kind other than
// human" sub-condition. multi: parses on Step regardless of kind (step.go),
// so it is not scoped to the human branch in checkRule8. Step.Multi has no
// declared-vs-default tracking (unlike Postcondition.Soft), so multi: false
// on a non-human step is treated as equivalent to "not declared" and only
// multi: true is flagged.
func checkRule8Multi(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Kind != "human" && s.Multi {
			*errs = append(*errs, stepErr(w, s.ID, "rule 8: multi: is only valid on kind: human; remove it"))
		}
	}
}

// checkRule9 implements §H rule 9.
func checkRule9(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Kind == "agentic" && len(s.Outcomes) > 0 {
			*errs = append(*errs, stepErr(w, s.ID, "rule 9: kind: agentic can only produce success/failure, not author-named outcomes; remove outcomes: and route from a following deterministic step that reads this step's writes: and prints a token"))
		}
	}
}

// checkRule9b implements §H rule 9b.
func checkRule9b(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.SubagentArgs == nil {
			continue
		}
		if _, ok := s.SubagentArgs.(map[string]any); !ok {
			*errs = append(*errs, stepErr(w, s.ID, "rule 9b: subagent_args: must be a map; change it to key: value pairs"))
		}
	}
}

// checkRule10 implements §H rule 10.
func checkRule10(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Emits != "" && s.Emits != "json" && s.Emits != "pairs" {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf("rule 10: emits: %q is not json or pairs; use one of the two", s.Emits)))
		}
	}
}

// checkRule11 implements §H rule 11.
func checkRule11(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		for _, k := range s.Writes.Keys {
			if _, isArg := w.Args[k]; isArg {
				continue // rule 5 already reports this
			}
			decl, ok := w.State[k]
			if !ok {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 11: writes: %q, which is not declared in state:; add state: {%s: {type: ...}} or fix the typo", k, k)))
				continue
			}
			if s.Writes.IsTyped {
				if t := s.Writes.Types[k]; t != "" && t != decl.Type {
					*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
						"rule 11: writes: %s: {type: %s} conflicts with state: %s: {type: %s}; make the types match", k, t, k, decl.Type)))
				}
			}
		}
	}
}

// checkRule12 implements §H rule 12. It reads the *Raw witnesses, not the
// defaulted MaxSteps/MaxVisits fields: applyDefaults (Finding F8) clamps an
// invalid raw value (e.g. max_steps: 0) to the default so the rest of the
// engine never sees it, which would otherwise make the defaulted field
// always look positive and this half of the rule unreachable.
func checkRule12(w *Workflow, errs *[]string) {
	if w.MaxStepsRaw != nil && *w.MaxStepsRaw < 1 {
		*errs = append(*errs, fileErr(w, fmt.Sprintf("rule 12: max_steps: %d is not a positive integer; use a value of 1 or more", *w.MaxStepsRaw)))
	}
	for _, s := range w.Steps {
		if s.MaxVisitsRaw != nil && *s.MaxVisitsRaw < 1 {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf("rule 12: max_visits: %d is not a positive integer; use a value of 1 or more", *s.MaxVisitsRaw)))
		}
	}
	for _, cycle := range findCycles(w) {
		allAbove := true
		for _, id := range cycle {
			step := w.StepByID(id)
			if step == nil || step.MaxVisits <= w.MaxSteps {
				allAbove = false
				break
			}
		}
		if allAbove {
			sorted := append([]string(nil), cycle...)
			sort.Strings(sorted)
			*errs = append(*errs, fileErr(w, fmt.Sprintf(
				"rule 12: cycle [%s] has max_visits: raised above max_steps: %d on every step; a cap that can never bind. Lower at least one step's max_visits: to at most max_steps:",
				strings.Join(sorted, ", "), w.MaxSteps)))
		}
	}
}

// findCycles returns every strongly-connected component of the step graph
// that forms a cycle: SCCs of size > 1, plus any single step with a
// self-loop. Terminal ids are leaves and never join a component.
func findCycles(w *Workflow) [][]string {
	index := map[string]int{}
	lowlink := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	counter := 0
	var sccs [][]string

	var strongconnect func(v string)
	strongconnect = func(v string) {
		index[v] = counter
		lowlink[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true

		if step := w.StepByID(v); step != nil {
			for _, t := range edgesOf(step) {
				if w.StepByID(t) == nil {
					continue // terminal
				}
				if _, seen := index[t]; !seen {
					strongconnect(t)
					if lowlink[t] < lowlink[v] {
						lowlink[v] = lowlink[t]
					}
				} else if onStack[t] {
					if index[t] < lowlink[v] {
						lowlink[v] = index[t]
					}
				}
			}
		}

		if lowlink[v] == index[v] {
			var scc []string
			for {
				n := len(stack) - 1
				top := stack[n]
				stack = stack[:n]
				onStack[top] = false
				scc = append(scc, top)
				if top == v {
					break
				}
			}
			if len(scc) > 1 {
				sccs = append(sccs, scc)
			} else if step := w.StepByID(scc[0]); step != nil {
				for _, t := range edgesOf(step) {
					if t == scc[0] {
						sccs = append(sccs, scc)
						break
					}
				}
			}
		}
	}

	for _, s := range w.Steps {
		if _, seen := index[s.ID]; !seen {
			strongconnect(s.ID)
		}
	}
	return sccs
}

// checkRule13 implements §H rule 13.
func checkRule13(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.AttemptsRaw != nil && *s.AttemptsRaw < 1 {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf("rule 13: attempts: %d is less than 1; use a value of 1 or more", *s.AttemptsRaw)))
		}
	}
}

// checkRule14 implements §H rule 14.
func checkRule14(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		p := s.Postcondition
		if p == nil {
			continue
		}
		if len(p.UnknownKeys) > 0 {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 14: postcondition: uses unknown key(s) %s; use only command, all_set, equals, and optional soft", strings.Join(p.UnknownKeys, ", "))))
		}
		if n := p.altCount(); n != 1 {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 14: postcondition: must use exactly one of command, all_set, equals (found %d); remove the extra key(s) or add the missing one", n)))
		}
	}
}

// checkRule18 implements §H rule 18: only the YAML tag "!cmd" makes a
// context: entry a command (internal/spec/context.go); everything else
// naming what looks like a command is a validation error, in either of two
// shapes an author actually wrote:
//
//   - A QUOTED string whose value merely starts with "!", e.g.
//     "!git diff main...HEAD". YAML resolves this to a plain "!!str" scalar
//     (ContextEntry.Tag == "!!str"), read as a FILE PATH literally named
//     "!git diff main...HEAD" — the case this rule originally covered, and
//     the live mistake found in review-pr's own dogfood run and several
//     docs/examples fixed alongside this rule.
//   - An UNQUOTED entry starting with "!", e.g. !git diff main. YAML reads
//     this not as that literal text but as a custom tag "!git" applied to
//     the scalar "diff main" (ContextEntry.Tag == "!git") — a shape that
//     silently passed validation before ContextEntry started capturing Tag,
//     since node.Decode discards the tag and leaves only the (wrong) bare
//     value indistinguishable from a plain file path.
//
// Both cases used to reach pawl run undetected, which then blocked at
// runtime with an opaque "no such file" error pointing at the whole command
// line (or, worse for the unquoted case, silently tried to read a file named
// after whatever text followed the tag). Catching both here, each with its
// own corrected !cmd form spelled out, turns that into an immediate,
// actionable validation error instead.
func checkRule18(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		for i, c := range s.Context {
			switch {
			case c.IsCmd:
				// c.Tag == "!cmd": already the one recognised command form.
			case c.Tag == "!!str" || c.Tag == "":
				if !strings.HasPrefix(c.Value, "!") {
					continue
				}
				if c.Value == "!cmd" || strings.HasPrefix(c.Value, "!cmd ") {
					// The author quoted the tag together with the command,
					// e.g. "!cmd git diff main" — a plain string whose
					// value happens to start with "!cmd ". Trimming only
					// the leading "!" (as the generic case below does)
					// would suggest !cmd "cmd git diff main", which runs a
					// program literally called "cmd". The tag has to sit
					// outside the quotes.
					rest := strings.TrimPrefix(strings.TrimPrefix(c.Value, "!cmd"), " ")
					*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
						"rule 18: context[%d]: %q is a plain string with the !cmd tag INSIDE the quotes, which names a FILE PATH, not a command; the tag must sit outside the quotes: !cmd %q",
						i, c.Value, rest)))
					continue
				}
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 18: context[%d]: %q is a plain string starting with \"!\", which names a FILE PATH, not a command; tag it with !cmd to run it as a command: !cmd %q",
					i, c.Value, strings.TrimPrefix(c.Value, "!"))))
			default:
				// Any other custom tag ("!git", "!bash", ...) on an
				// unquoted entry: YAML has already stripped the "!name"
				// prefix off into c.Tag, so the corrected form re-attaches
				// the tag name (minus its leading "!") to the front of the
				// remaining value under !cmd.
				suggested := strings.TrimPrefix(c.Tag, "!") + " " + c.Value
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 18: context[%d]: tagged %s %q, which is not a recognised command form; only the YAML tag !cmd runs a command — use !cmd %q instead",
					i, c.Tag, c.Value, suggested)))
			}
		}
	}
}
