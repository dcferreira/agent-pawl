package engine

import (
	"fmt"

	"github.com/dcferreira/agentic-workflow-fsm/internal/journal"
	"github.com/dcferreira/agentic-workflow-fsm/internal/render"
	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// declaredType returns the state: or args: type declared for key, or ""
// if key is neither (a pseudo-key, or unknown — callers only ask for keys
// they know are declared or pseudo).
func declaredType(w *spec.Workflow, key string) string {
	if a, ok := w.Args[key]; ok {
		return a.Type
	}
	if s, ok := w.State[key]; ok {
		return s.Type
	}
	return ""
}

// renderValue wraps v as a render.Value per its declared type: "json" is a
// render.JSONValue (pretty-printed in prose, compact in shell/env), anything
// else a render.StringValue of its plain text form.
func renderValue(declType string, v any) render.Value {
	if declType == "json" {
		return render.JSONValue(v)
	}
	if v == nil {
		return render.StringValue("")
	}
	if s, ok := v.(string); ok {
		return render.StringValue(s)
	}
	return render.StringValue(fmt.Sprint(v))
}

// buildValues assembles the render.Values available at cur: every declared
// arg and state key at its current value, plus the six engine pseudo-keys
// (design/format-spec.md §B.2). visits is the entering step's visit count so
// far (before this entry).
func buildValues(w *spec.Workflow, rs *journal.RunState, runID, step string, attempt, visits int) render.Values {
	vals := render.Values{}
	for k, decl := range w.State {
		v, ok := rs.State[k]
		if !ok {
			if decl.Default == nil {
				continue
			}
			v = *decl.Default
		}
		vals[k] = renderValue(decl.Type, v)
	}
	for k, v := range rs.Args {
		vals[k] = renderValue(declaredType(w, k), v)
	}
	for k, v := range rs.State {
		vals[k] = renderValue(declaredType(w, k), v)
	}
	vals["run_id"] = render.StringValue(runID)
	vals["step"] = render.StringValue(step)
	vals["attempt"] = render.StringValue(fmt.Sprint(attempt))
	vals["visits"] = render.StringValue(fmt.Sprint(visits))
	vals["last_error"] = render.StringValue(rs.LastError)
	vals["blocked_reason"] = render.StringValue(rs.BlockedReason)
	return vals
}
