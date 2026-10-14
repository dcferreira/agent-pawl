package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
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
	return render.StringValue(formatScalar(declType, v))
}

// formatScalar renders a non-string, non-json state/arg value as text by its
// declared type, never in exponent form: journal replay hands numbers back
// as float64, whose fmt.Sprint form is "1e+06" for 1000000. An integer
// renders as a plain decimal integer; a number as the shortest exact decimal.
func formatScalar(declType string, v any) string {
	switch x := v.(type) {
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		if declType == "number" {
			if f, err := x.Float64(); err == nil {
				return strconv.FormatFloat(f, 'f', -1, 64)
			}
			return x.String()
		}
		if i, err := x.Int64(); err == nil {
			return strconv.FormatInt(i, 10)
		}
		return x.String()
	case float64:
		if declType == "integer" && x == math.Trunc(x) && math.Abs(x) < 1<<63 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
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
