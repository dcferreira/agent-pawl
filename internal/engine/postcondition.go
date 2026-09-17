package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dcferreira/agentic-workflow-fsm/internal/render"
	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// checkResult is the outcome of evaluating a postcondition or an agentic
// return-schema validation, whichever ran: they feed the same attempt-retry
// machinery (design/format-spec.md §B.4, §B.7).
type checkResult struct {
	OK   bool
	Text string
	Soft bool
}

// evaluatePostcondition evaluates step's postcondition, always in this
// process or a subprocess it spawns — never by the actor that did the work
// (DESIGN.md §5). A nil postcondition (legal on deterministic/wait/human,
// design/format-spec.md §B.7) is vacuously satisfied. raw is the current
// run's args+state, unrendered, for all_set/equals' own key lookups; vals is
// the same data as render.Values, for ${key} substitution in command/equals.
func (e *Engine) evaluatePostcondition(step *spec.Step, raw map[string]any, vals render.Values) (checkResult, error) {
	pc := step.Postcondition
	if pc == nil {
		return checkResult{OK: true}, nil
	}
	soft := pc.Soft
	switch {
	case pc.HasCommand:
		return e.evaluateCommandPostcondition(pc.Command, vals, soft)
	case pc.HasAllSet:
		return evaluateAllSet(pc.AllSet, raw, soft), nil
	case pc.HasEquals:
		return evaluateEquals(pc.Equals, raw, vals, soft), nil
	default:
		return checkResult{}, fmt.Errorf("engine: step %q: postcondition has none of command/all_set/equals (validator should have rejected this)", step.ID)
	}
}

func (e *Engine) evaluateCommandPostcondition(tmpl string, vals render.Values, soft bool) (checkResult, error) {
	cmd, err := resolveScriptPathTemplate(tmpl, vals, filepath.Dir(e.Workflow.Path))
	if err != nil {
		return checkResult{}, fmt.Errorf("engine: rendering postcondition command: %w", err)
	}
	out, errOut, exit, err := e.execShell(cmd, render.Keys(tmpl), vals)
	if err != nil {
		if errors.Is(err, errTimeout) {
			return checkResult{OK: false, Soft: soft, Text: "postcondition exceeded the wall-clock ceiling"}, nil
		}
		return checkResult{}, fmt.Errorf("engine: running postcondition command: %w", err)
	}
	if exit == 0 {
		return checkResult{OK: true, Soft: soft}, nil
	}
	text := strings.TrimSpace(out)
	if text == "" {
		text = strings.TrimSpace(errOut)
	}
	return checkResult{OK: false, Soft: soft, Text: text}, nil
}

// isEmptyValue reports whether v counts as "not set" for all_set:
// (design/format-spec.md §D): absent, nil, the zero value of its type, or an
// empty string/slice/map.
func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case int64:
		return x == 0
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

func evaluateAllSet(keys []string, raw map[string]any, soft bool) checkResult {
	for _, k := range keys {
		v, ok := raw[k]
		if !ok || isEmptyValue(v) {
			return checkResult{OK: false, Soft: soft, Text: fmt.Sprintf("all_set: key %q is not set", k)}
		}
	}
	return checkResult{OK: true, Soft: soft}
}

func evaluateEquals(want map[string]any, raw map[string]any, vals render.Values, soft bool) checkResult {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		expectedRaw := want[k]
		expected := fmt.Sprint(expectedRaw)
		if s, ok := expectedRaw.(string); ok {
			if rendered, err := render.RenderProse(s, vals); err == nil {
				expected = rendered
			}
		}
		actual := fmt.Sprint(raw[k])
		if _, ok := raw[k]; !ok {
			actual = ""
		}
		if actual != expected {
			return checkResult{OK: false, Soft: soft, Text: fmt.Sprintf("equals: %q is %q, want %q", k, actual, expected)}
		}
	}
	return checkResult{OK: true, Soft: soft}
}
