package spec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/render"
)

// This file holds the validator rules for kind: parallel's foreach:
// (design/format-spec.md §B.15, §H rules 22-28). A foreach step runs one
// deterministic body step once per item of a json state key's array and
// joins to success, partial or failure; its body writes are captured per item
// into the step's collect: key and never become global state.

// foreachBodyOwners maps a foreach body's step id to the id of the foreach
// step that declares it (first declared wins, as in branchStepIDs). Only
// kind: parallel steps count; a foreach: on any other kind is reported by
// rule 22 and claims nothing.
func foreachBodyOwners(w *Workflow) map[string]string {
	owner := map[string]string{}
	for _, s := range w.Steps {
		if s.Kind != "parallel" || s.Foreach == nil || s.Foreach.Body == "" {
			continue
		}
		if _, claimed := owner[s.Foreach.Body]; !claimed {
			owner[s.Foreach.Body] = s.ID
		}
	}
	return owner
}

// foreachSteps returns the kind: parallel steps that declare foreach: (and
// not also branches:, which rule 22 rejects wholesale), in declaration order.
func foreachSteps(w *Workflow) []*Step {
	var out []*Step
	for i := range w.Steps {
		s := &w.Steps[i]
		if s.Kind == "parallel" && s.Foreach != nil && len(s.Branches) == 0 {
			out = append(out, s)
		}
	}
	return out
}

// checkForeachShape implements §H rule 22: a kind: parallel step declares
// exactly one of branches:/foreach:; foreach: is valid only on kind:
// parallel; its own keys are over/body/collect/max_items, the first three
// required and max_items, when set, at least 1. (The ≥ 2 entries rule for
// branches: is checkParallelBranches's, and applies only when branches: is
// the form used.)
func checkForeachShape(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if s.Kind != "parallel" {
			if s.Foreach != nil {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 22: foreach: is only valid on kind: parallel, not kind: %s; remove the foreach: block", s.Kind)))
			}
			continue
		}
		hasBranches, hasForeach := len(s.Branches) > 0, s.Foreach != nil
		switch {
		case hasBranches && hasForeach:
			*errs = append(*errs, stepErr(w, s.ID,
				"rule 22: kind: parallel declares both branches: and foreach:; keep exactly one"))
			continue
		case !hasBranches && !hasForeach:
			*errs = append(*errs, stepErr(w, s.ID,
				"rule 22: kind: parallel requires exactly one of branches: or foreach:; add branches: [a, b] or foreach: {over: <key>, body: <step>, collect: <key>}"))
			continue
		case !hasForeach:
			continue
		}

		f := s.Foreach
		if len(f.UnknownKeys) > 0 {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 22: foreach: uses unknown key(s) %s; use only over, body, collect and max_items", strings.Join(f.UnknownKeys, ", "))))
		}
		if f.Over == "" {
			*errs = append(*errs, stepErr(w, s.ID, "rule 22: foreach.over: is required; add over: <json state key holding the array>"))
		}
		if f.Body == "" {
			*errs = append(*errs, stepErr(w, s.ID, "rule 22: foreach.body: is required; add body: <id of the step run once per item>"))
		}
		if f.Collect == "" {
			*errs = append(*errs, stepErr(w, s.ID, "rule 22: foreach.collect: is required; add collect: <json state key receiving the per-item results>"))
		}
		if f.MaxItemsSet && f.MaxItems < 1 {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 22: foreach.max_items: %d is less than 1; use a value of 1 or more, or omit it for the default of %d", f.MaxItems, DefaultForeachMaxItems)))
		}
	}
}

// checkForeachOverCollectKey reports (rule 23) a foreach.over/collect name
// that is not a declared json state: key.
func checkForeachOverCollectKey(w *Workflow, errs *[]string, s *Step, field, key string) {
	if key == "" {
		return // rule 22 already reported the missing field
	}
	if _, isArg := w.Args[key]; isArg {
		*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
			"rule 23: foreach.%s: %q is declared under args:, but foreach.%s must name a state: key; declare it under state: {%s: {type: json}}", field, key, field, key)))
		return
	}
	decl, ok := w.State[key]
	switch {
	case !ok:
		*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
			"rule 23: foreach.%s: %q does not name a declared state: key; declare state: {%s: {type: json}}", field, key, key)))
	case decl.Type != "json":
		*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
			"rule 23: foreach.%s: %q has type %s, but foreach.%s must be type: json (an array)", field, key, decl.Type, field)))
	}
}

// checkForeachOverCollect implements §H rule 23: foreach.over and
// foreach.collect each name a declared json state: key (not an arg, not
// another type); collect differs from over and is written by no step's
// writes: — the foreach step itself is collect's one writer.
func checkForeachOverCollect(w *Workflow, errs *[]string) {
	for _, s := range foreachSteps(w) {
		f := s.Foreach
		checkForeachOverCollectKey(w, errs, s, "over", f.Over)
		checkForeachOverCollectKey(w, errs, s, "collect", f.Collect)
		if f.Collect != "" && f.Collect == f.Over {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 23: foreach.collect: %q is the same key as foreach.over; use a different key to receive the results", f.Collect)))
		}
		if f.Collect == "" {
			continue
		}
		for _, other := range w.Steps {
			for _, k := range other.Writes.Keys {
				if k == f.Collect {
					*errs = append(*errs, stepErr(w, other.ID, fmt.Sprintf(
						"rule 23: writes: %q, but %q is the collect: key of foreach step %q, which is its only writer; use a different key", k, k, s.ID)))
				}
			}
		}
	}
}

// checkForeachBody implements §H rule 24: foreach.body names a declared
// deterministic or agentic step (any other kind is "no nesting"), that is not
// start:, is owned by no other parallel step (as a branch or another
// foreach's body), and declares none of next:/outcomes:/catch:/max_visits:
// (nor, for a deterministic body, attempts:/attempt_key:). Unlike a branch, a
// body MAY declare retry: and postcondition:, and an agentic body MAY declare
// attempts:/attempt_key: (a per-item budget).
func checkForeachBody(w *Workflow, errs *[]string) {
	owner := branchStepIDs(w)
	for _, s := range foreachSteps(w) {
		id := s.Foreach.Body
		if id == "" {
			continue
		}
		body := w.StepByID(id)
		switch {
		case body == nil:
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 24: foreach.body: %q does not name a declared step; declare step %q or fix the typo", id, id)))
			continue
		case body.Kind != "deterministic" && body.Kind != "agentic":
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 24: foreach.body: step %q has kind %q, but a foreach body must be deterministic or agentic (no nesting)", id, body.Kind)))
		}

		if id == w.Start {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 24: foreach.body: step %q is start:, but a foreach body may not be the workflow's start step", id)))
		}
		if first := owner[id]; first != "" && first != s.ID {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 24: foreach.body: step %q is already claimed by parallel step %q; a step may be owned by only one parallel step", id, first)))
		}
		for _, f := range ownedStepForbiddenFields {
			if f.has(*body) {
				if f.field == "attempts:" || f.field == "attempt_key:" {
					// An agentic body owns a per-item attempt budget.
					if body.Kind == "agentic" {
						continue
					}
					*errs = append(*errs, stepErr(w, id, fmt.Sprintf(
						"rule 24: declares %s, but it is a deterministic foreach body of parallel step %q; a deterministic body has retry: for hard failures, and %s applies only to an agentic body; remove %s", f.field, s.ID, f.field, f.field)))
					continue
				}
				*errs = append(*errs, stepErr(w, id, fmt.Sprintf(
					"rule 24: declares %s, but it is the foreach body of parallel step %q, which owns routing for every item; remove %s", f.field, s.ID, f.field)))
			}
		}
	}
}

// keyRead is one ${key} read the validator can attribute to a source: the
// step that reads it ("" for a terminal message or an invariant's check) and
// a function that wraps a message with that source's error prefix.
type keyRead struct {
	stepID string
	loc    string
	key    string
	wrap   func(msg string) string
}

// allKeyReads lists every ${key} read the validator derives, in a stable
// order: each step's templates (stepTemplates), each terminal's message and
// each invariant's check.
func allKeyReads(w *Workflow) []keyRead {
	var out []keyRead
	add := func(stepID, loc, text string, wrap func(string) string) {
		for _, k := range render.Keys(text) {
			out = append(out, keyRead{stepID, loc, k, wrap})
		}
	}
	for _, s := range w.Steps {
		s := s
		wrap := func(msg string) string { return stepErr(w, s.ID, msg) }
		for _, t := range stepTemplates(s) {
			add(s.ID, t.loc, t.text, wrap)
		}
	}
	terminalIDs := make([]string, 0, len(w.Terminal))
	for id := range w.Terminal {
		terminalIDs = append(terminalIDs, id)
	}
	sort.Strings(terminalIDs)
	for _, id := range terminalIDs {
		id := id
		add("", "message", w.Terminal[id].Message, func(msg string) string { return terminalErr(w, id, msg) })
	}
	for i, inv := range w.Invariants {
		add("", fmt.Sprintf("%s: check", invariantRef(inv, i)), inv.Check, func(msg string) string { return fileErr(w, msg) })
	}
	return out
}

// checkForeachBodyWrites implements §H rule 25. A body's writes: keys are
// captured per item into collect: and never become global state, so no step
// other than the body may read one (anywhere the validator derives reads:
// a step's templates, a terminal message, an invariant's check, another
// foreach's over:), and no other step may write one.
func checkForeachBodyWrites(w *Workflow, errs *[]string) {
	// bodyKey: key -> (body step id, its foreach step) for every key a
	// foreach body writes; the first body to claim a key names it.
	type origin struct{ body, foreach, collect string }
	bodyKey := map[string]origin{}
	for _, s := range foreachSteps(w) {
		body := w.StepByID(s.Foreach.Body)
		if body == nil {
			continue
		}
		for _, k := range body.Writes.Keys {
			if _, ok := bodyKey[k]; !ok {
				bodyKey[k] = origin{body.ID, s.ID, s.Foreach.Collect}
			}
		}
	}
	if len(bodyKey) == 0 {
		return
	}

	for _, r := range allKeyReads(w) {
		o, ok := bodyKey[r.key]
		if !ok || r.stepID == o.body {
			continue
		}
		*errs = append(*errs, r.wrap(fmt.Sprintf(
			"rule 25: %s uses ${%s}, but %q is written only by foreach body %q and captured per item into collect: key %q, never into global state; read %q after the join instead",
			r.loc, r.key, r.key, o.body, o.collect, o.collect)))
	}
	for _, s := range foreachSteps(w) {
		if s.Foreach.Over == "" {
			continue
		}
		if o, ok := bodyKey[s.Foreach.Over]; ok {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 25: foreach.over: %q is written only by foreach body %q and captured per item into collect: key %q, never into global state; iterate over a key another step writes",
				s.Foreach.Over, o.body, o.collect)))
		}
	}
	for _, s := range w.Steps {
		for _, k := range s.Writes.Keys {
			if o, ok := bodyKey[k]; ok && s.ID != o.body {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 25: writes: %q, which is also written by foreach body %q; body writes are captured per item into collect:, so no other step may write the same key — use a different key", k, o.body)))
			}
		}
	}
}

// checkForeachRouting implements §H rule 26. A foreach step resolves to
// success, partial or failure (plus the reserved exhausted); partial must be
// routed explicitly, by an outcomes: entry or a catch: rule. A branches:
// parallel step still resolves only to success/failure and may not route
// partial.
func checkForeachRouting(w *Workflow, errs *[]string) {
	routesPartial := func(s Step) bool {
		if _, ok := s.Outcomes["partial"]; ok {
			return true
		}
		for _, c := range s.Catch {
			if c.On == "partial" {
				return true
			}
		}
		return false
	}
	for _, s := range w.Steps {
		if s.Kind != "parallel" {
			continue
		}
		switch {
		case s.Foreach != nil && len(s.Branches) == 0:
			if !routesPartial(s) {
				*errs = append(*errs, stepErr(w, s.ID,
					"rule 26: a foreach step resolves to success, partial or failure; route partial explicitly — add outcomes: {success: ..., partial: <step-or-terminal>, failure: ...} or catch: [{on: partial, next: ...}]"))
			}
		case len(s.Branches) > 0 && s.Foreach == nil:
			if routesPartial(s) {
				*errs = append(*errs, stepErr(w, s.ID,
					"rule 26: kind: parallel with branches: resolves only to success or failure, so it cannot route partial; remove the partial route (only foreach: produces partial)"))
			}
		}
	}
}

// itemKeyScopeMsg is rule 27's message for ${item}/${item_index} read
// outside a foreach body's own fields; checkRule4 emits it, since it is the
// one place that already walks every renderable field.
func itemKeyScopeMsg(loc, key string) string {
	return fmt.Sprintf(
		"rule 27: %s uses ${%s}, which is only valid in a foreach body's own fields; remove it or move the work into the body", loc, key)
}

// checkForeachItemKeys implements the reservation half of §H rule 27: item
// and item_index are engine pseudo-keys scoped to a foreach body, so they may
// not be declared as state: or args: keys (checkRule4 reports their use
// outside a body).
func checkForeachItemKeys(w *Workflow, errs *[]string) {
	check := func(section string, keys []string) {
		sort.Strings(keys)
		for _, k := range keys {
			if isForeachItemKey(k) {
				*errs = append(*errs, fileErr(w, fmt.Sprintf(
					"rule 27: %s: key %q is reserved for foreach bodies; rename it", section, k)))
			}
		}
	}
	stateKeys := make([]string, 0, len(w.State))
	for k := range w.State {
		stateKeys = append(stateKeys, k)
	}
	check("state", stateKeys)
	argKeys := make([]string, 0, len(w.Args))
	for k := range w.Args {
		argKeys = append(argKeys, k)
	}
	check("args", argKeys)
}

// itemShellExec matches a shell -c whose script argument is a bare
// ${item}/${item_index} on the UNRENDERED template — `sh -c ${item}`,
// `bash -ec "${item}"` — the idiom rule 28 forbids. Deliberately simple: it
// does not try to catch every way to execute a value (eval, xargs sh, ...).
var itemShellExec = regexp.MustCompile(`(?:^|\s)-[A-Za-z]*c\s+["']?\$\{(item|item_index)\}`)

// checkForeachInjection implements §H rule 28: in a foreach body's shell
// contexts (run:, postcondition.command, and an agentic body's !cmd context: entries), `-c ${item}` / `-c ${item_index}`
// is an error. ${item} comes from workflow state; the `sh -c ${key}` idiom
// is legitimate only for an args: value (AGENTS.md, "${key} shell-quotes as
// a single token"), because piping state through `sh -c` turns whatever
// produced that state into arbitrary command execution.
func checkForeachInjection(w *Workflow, errs *[]string) {
	bodies := foreachBodyOwners(w)
	for _, s := range w.Steps {
		if _, ok := bodies[s.ID]; !ok {
			continue
		}
		fields := []stepTemplate{{"run", s.Run}}
		for i, c := range s.Context {
			if c.IsCmd {
				fields = append(fields, stepTemplate{fmt.Sprintf("context[%d]", i), c.Value})
			}
		}
		if s.Postcondition != nil {
			fields = append(fields, stepTemplate{"postcondition.command", s.Postcondition.Command})
		}
		for _, f := range fields {
			if m := itemShellExec.FindStringSubmatch(f.text); m != nil {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 28: %s runs ${%s} as a script (-c); ${%s} comes from workflow state and must never be executed as a script — pass it as an argument instead, e.g. ./process.sh ${%s}",
					f.loc, m[1], m[1], m[1])))
			}
		}
	}
}
