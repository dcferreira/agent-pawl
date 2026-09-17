package engine

import (
	"fmt"

	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// reservedRoutable are the reserved outcome tokens routed via the
// catch-or-default mechanism (design/format-spec.md §B.11): "failure",
// handled by catch: (default failure -> blocked), and "exhausted", produced
// on hitting max_visits:/max_steps: (default blocked). Both are exempt from
// needing a route in outcomes:, though an author may still add one there.
func isReservedRoutable(outcome string) bool {
	return outcome == "failure" || outcome == "exhausted"
}

// resolveTarget is the outcome-resolution table required by DESIGN.md §6:
// (step, outcome) -> target, built as one function over an explicit lookup
// rather than scattered ifs, so it is unit-testable as a table.
//
// Finding I4: design/format-spec.md §D defines catch: as an ordered list of
// {on: <outcome>, next: …} — not {on: failure|exhausted} — so an author may
// route *any* outcome, not just the two reserved ones, through catch:.
// catch: is consulted, in declared order, for any outcome outcomes: doesn't
// already route.
//
// viaCatch is true whenever the route actually came off the catch: list, or
// off the engine-wide default for the two reserved-routable tokens (failure,
// exhausted) when neither outcomes: nor catch: names them. For those two
// tokens this always coincides with "the postcondition did not pass (or was
// never evaluated, on a hard failure)", which is what design/format-spec.md
// §B.4's clearing rule ("a non-catch edge") depends on. An author may also
// route a non-reserved outcome through catch: (I4); that edge is viaCatch in
// the literal, YAML-construct sense even though the postcondition passed to
// produce that outcome — see TestResolveTarget for the resulting cases.
func resolveTarget(step *spec.Step, outcome string) (target string, viaCatch bool, err error) {
	if t, ok := step.Outcomes[outcome]; ok {
		return t, isReservedRoutable(outcome), nil
	}
	for _, c := range step.Catch {
		if c.On == outcome {
			return c.Next, true, nil
		}
	}
	if isReservedRoutable(outcome) {
		return "blocked", true, nil
	}
	if step.Next != "" {
		return step.Next, false, nil
	}
	return "", false, fmt.Errorf("engine: step %q: no next:, outcomes: or catch: route for outcome %q (validator should have rejected this)", step.ID, outcome)
}
