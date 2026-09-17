package spec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dcferreira/agentic-workflow-fsm/internal/render"
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
// 6, 9, 9b, 10, 11, 12, 13, 14 (Ruling R4), plus the R3 kind rejections
// (wait, human, parallel) and the R8 guards:/invariants: rejection. Rules 7,
// 8, 15, 16 and the two warnings are deferred per R4.
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
	checkGuardsInvariants(w, &errs)
	checkRetry(w, &errs)
	checkKindSupport(w, &errs)
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
	checkRule9(w, &errs)
	checkRule9b(w, &errs)
	checkRule10(w, &errs)
	checkRule11(w, &errs)
	checkRule12(w, &errs)
	checkRule13(w, &errs)
	checkRule14(w, &errs)

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

// checkGuardsInvariants rejects guards:/invariants: (Ruling R8): they are
// deferred, and silently disabling enforcement is the failure class
// DESIGN.md §5 exists to prevent, so a declared block is rejected outright
// rather than ignored.
func checkGuardsInvariants(w *Workflow, errs *[]string) {
	if len(w.Guards) > 0 {
		*errs = append(*errs, fileErr(w, "guards: is not implemented in this build; remove the guards: block"))
	}
	if len(w.Invariants) > 0 {
		*errs = append(*errs, fileErr(w, "invariants: is not implemented in this build; remove the invariants: block"))
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
func checkKindSupport(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		switch s.Kind {
		case "deterministic", "agentic":
			// supported
		case "wait", "human":
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"kind %q is not implemented in this build (milestone 1 MVP covers deterministic and agentic)", s.Kind)))
		case "parallel":
			*errs = append(*errs, stepErr(w, s.ID, "kind: parallel is reserved for Milestone 3"))
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
	return out
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

// checkRule3 implements §H rule 3.
func checkRule3(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
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
