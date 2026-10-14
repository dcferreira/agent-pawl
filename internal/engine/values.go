package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

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
// declared type, never in exponent form: a float64 renders via fmt.Sprint as
// "1e+06" for 1000000. (Journal replay hands numbers back as json.Number,
// like the fresh submit path; float64 still arises from number-typed zero
// defaults and YAML defaults.) An integer
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

// canonicalNumber renders a numeric Go value (int, int64, uint64, float64 or
// json.Number) as one canonical decimal string, so numerically equal values
// written differently ("1e6", 1000000, 1000000.0) compare equal. ok is false
// for a non-numeric value or unparseable json.Number text.
//
// Plain-integer json.Number text goes through big.Int (exact, cheap, no
// exponent expansion). Everything else goes through float64; text is never
// fed to big.Rat, so a hostile "1e999999999" from step stdout cannot cost
// time or memory. Integers beyond 2^53 written in exponent/decimal form
// (e.g. "9.007199254740993e15") may therefore compare inexactly; accepted.
func canonicalNumber(v any) (string, bool) {
	switch x := v.(type) {
	case int:
		return strconv.FormatInt(int64(x), 10), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	case float64:
		return canonicalFloat(x), true
	case json.Number:
		s := string(x)
		if isPlainInteger(s) {
			if n, ok := new(big.Int).SetString(s, 10); ok {
				return n.String(), true
			}
			return "", false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			var ne *strconv.NumError
			if errors.As(err, &ne) && ne.Err == strconv.ErrRange {
				return canonicalFloat(f), true // +/-Inf (overflow) or a signed zero (underflow)
			}
			return "", false
		}
		return canonicalFloat(f), true
	}
	return "", false
}

func canonicalFloat(f float64) string {
	if !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) && math.Abs(f) < 1<<63 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// isPlainInteger reports whether s is an optional '-' followed by one or more
// ASCII digits only (no '.', no exponent).
func isPlainInteger(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// jsonNumberIsZero reports whether n numerically equals zero ("0", "0.0",
// "-0", "0e5") by inspecting its mantissa digits — no parsing, so an
// enormous exponent costs nothing and "1e-999999999" is correctly non-zero.
func jsonNumberIsZero(n json.Number) bool {
	s := strings.TrimPrefix(string(n), "-")
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	digits := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '.':
		case s[i] >= '1' && s[i] <= '9':
			return false
		case s[i] == '0':
			digits++
		default:
			return false
		}
	}
	return digits > 0
}
