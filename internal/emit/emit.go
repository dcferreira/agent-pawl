// Package emit implements design/format-spec.md §B.1, the one stdout grammar
// shared by deterministic and wait steps: it turns a command's captured exit
// code and stdout into a typed Result, escaping C0 control characters at
// this one boundary so a value arriving through a step's stdout can never
// corrupt a single-line journal record downstream.
//
// emit depends on internal/spec (for the Step and StateDecl types it reads
// declarations from) and on nothing else internal.
package emit

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dcferreira/agentic-workflow-fsm/internal/spec"
)

// ErrParse marks a Parse failure: the captured stdout was unintelligible
// under §B.1, which is a workflow authoring bug distinct from a legitimate
// "failure" Result. Callers can test it with errors.Is.
var ErrParse = errors.New("emit: unintelligible stdout")

// reservedOutcomes are the outcome names design/format-spec.md §C reserves;
// declaring only these under outcomes: means a step declares no
// author-named outcome, so §B.1 expects no TOKEN.
var reservedOutcomes = map[string]bool{
	"success":   true,
	"failure":   true,
	"timeout":   true,
	"exhausted": true,
	"chosen":    true,
}

// Result is the parsed outcome of a step's captured stdout.
type Result struct {
	// Outcome is the reserved "success" or "failure", or an author-named
	// outcome routed by the step's outcomes: map.
	Outcome string
	// Writes holds the values parsed from the payload, keyed by state key
	// name and coerced to each key's declared type. Nil when the line
	// carried no payload, in which case declared keys keep their previous
	// values (design/format-spec.md §B.1).
	Writes map[string]any
}

// Parse interprets a step's captured stdout per design/format-spec.md §B.1.
//
// Non-zero exitCode is always the "failure" outcome, whatever was printed;
// stdout is not inspected. On exit 0, the last non-empty line of stdout
// (trailing whitespace does not count) is read: a TOKEN is present if and
// only if step declares at least one author-named outcome (a name other
// than the reserved success/failure/timeout/exhausted/chosen), in which
// case the first whitespace-separated field is the token and the routed
// outcome it names, and the rest of the line is the payload; otherwise the
// whole line is payload and the outcome is "success".
//
// The payload is parsed per step.Emits ("json" or "pairs"); its keys must
// be a subset of step.Writes.Keys. decls supplies the declared state: type
// for a key written under an untyped (list-form) writes:; for a typed
// (map-form) writes: (as agentic steps require, but deterministic and wait
// steps may also use), step.Writes.Types supplies it and decls may be nil.
//
// A non-nil error means the line was unintelligible: it wraps ErrParse and
// names the step, the offending token or key, and the routes or keys that
// do exist, and is never returned alongside a usable Result.
func Parse(stdout string, exitCode int, step *spec.Step, decls map[string]spec.StateDecl) (Result, error) {
	if exitCode != 0 {
		return Result{Outcome: "failure"}, nil
	}

	line := lastNonEmptyLine(stdout)
	named := hasAuthorNamedOutcomes(step)

	var token, payload string
	if named {
		token, payload = splitToken(line)
	} else {
		payload = line
	}

	outcome := "success"
	if named {
		if token == "" {
			return Result{}, fmt.Errorf("%w: step %q declares outcomes %s but its stdout carried no TOKEN on the last non-empty line",
				ErrParse, step.ID, sortedOutcomeNames(step))
		}
		if _, ok := step.Outcomes[token]; !ok {
			return Result{}, fmt.Errorf("%w: step %q: outcome token %q is not routed by any outcomes: entry; declared outcomes are %s",
				ErrParse, step.ID, token, sortedOutcomeNames(step))
		}
		outcome = token
	}

	writes, err := parsePayload(step, decls, payload)
	if err != nil {
		return Result{}, err
	}

	return Result{Outcome: outcome, Writes: writes}, nil
}

// hasAuthorNamedOutcomes reports whether step declares any outcome name
// other than the reserved set (design/format-spec.md §B.1, §C).
func hasAuthorNamedOutcomes(step *spec.Step) bool {
	for name := range step.Outcomes {
		if !reservedOutcomes[name] {
			return true
		}
	}
	return false
}

func sortedOutcomeNames(step *spec.Step) string {
	names := make([]string, 0, len(step.Outcomes))
	for k := range step.Outcomes {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// lastNonEmptyLine returns the last line of stdout that is non-empty after
// trimming trailing whitespace (a line of only spaces counts as empty), with
// that trailing whitespace (including a CRLF's "\r") trimmed off. It returns
// "" if stdout has no non-empty line.
func lastNonEmptyLine(stdout string) string {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], " \t\r")
		if line != "" {
			return line
		}
	}
	return ""
}

// splitToken splits line into its leading whitespace-separated TOKEN and the
// remaining payload (with leading whitespace trimmed).
func splitToken(line string) (token, payload string) {
	idx := strings.IndexAny(line, " \t")
	if idx < 0 {
		return line, ""
	}
	token = line[:idx]
	payload = strings.TrimLeft(line[idx:], " \t")
	return token, payload
}

// parsePayload parses payload per step.Emits, returning nil (no error) for
// an empty payload: a TOKEN-only line is valid under both modes and writes
// nothing (design/format-spec.md §B.1). It also returns nil, nil without
// attempting to parse anything when the step declares no writes: keys: a
// step that writes nothing has nothing to parse out of its payload, so a
// non-empty last line is just log text, not an authoring bug (Finding F4).
func parsePayload(step *spec.Step, decls map[string]spec.StateDecl, payload string) (map[string]any, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || len(step.Writes.Keys) == 0 {
		return nil, nil
	}
	if step.Emits == "pairs" {
		return parsePairs(step, decls, payload)
	}
	return parseJSON(step, decls, payload)
}

// keyDeclared reports whether key is among step.Writes.Keys.
func keyDeclared(step *spec.Step, key string) bool {
	for _, k := range step.Writes.Keys {
		if k == key {
			return true
		}
	}
	return false
}

// declaredType returns the declared state: type for key, from the step's
// typed writes: map if it has one, otherwise from decls (the workflow's
// state: declarations). "" means no type is known.
func declaredType(step *spec.Step, decls map[string]spec.StateDecl, key string) string {
	if step.Writes.IsTyped {
		return step.Writes.Types[key]
	}
	if decls == nil {
		return ""
	}
	return decls[key].Type
}

func declaredKeysList(step *spec.Step) string {
	if len(step.Writes.Keys) == 0 {
		return "(none)"
	}
	return strings.Join(step.Writes.Keys, ", ")
}

// parseJSON parses payload as a JSON object per design/format-spec.md §B.1's
// "json" emits mode.
func parseJSON(step *spec.Step, decls map[string]spec.StateDecl, payload string) (map[string]any, error) {
	// sanitizeJSONStrings only touches control bytes inside string literals
	// (illegal per RFC 8259, and rejected outright by Go's decoder),
	// leaving a script's own tab/newline-formatted JSON untouched (Finding
	// F1); escapeValue/EscapeC0 below re-neutralise decoded string values
	// (and, recursively, map keys) for storage regardless of how they got
	// there (Finding F2).
	sanitized := sanitizeJSONStrings(payload)

	dec := json.NewDecoder(strings.NewReader(sanitized))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: step %q: payload is not valid JSON: %v", ErrParse, step.ID, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: step %q: payload has trailing data after its JSON value", ErrParse, step.ID)
	}

	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: step %q: payload is valid JSON but not an object", ErrParse, step.ID)
	}

	writes := make(map[string]any, len(obj))
	for key, v := range obj {
		if !keyDeclared(step, key) {
			return nil, fmt.Errorf("%w: step %q: payload key %q is not declared in writes: (declared keys: %s)",
				ErrParse, step.ID, key, declaredKeysList(step))
		}
		dt := declaredType(step, decls, key)
		if dt == "" {
			return nil, fmt.Errorf("%w: step %q: key %q has no declared state type in writes: or state:", ErrParse, step.ID, key)
		}
		coerced, err := coerceFromJSON(step.ID, key, v, dt)
		if err != nil {
			return nil, err
		}
		writes[key] = coerced
	}
	return writes, nil
}

// parsePairs parses payload as whitespace-separated k=v tokens per
// design/format-spec.md §B.1's "pairs" emits mode.
func parsePairs(step *spec.Step, decls map[string]spec.StateDecl, payload string) (map[string]any, error) {
	fields := strings.Fields(payload)
	writes := make(map[string]any, len(fields))
	for _, f := range fields {
		idx := strings.IndexByte(f, '=')
		if idx < 0 {
			return nil, fmt.Errorf("%w: step %q: pairs payload field %q is not key=value", ErrParse, step.ID, f)
		}
		key := f[:idx]
		val := f[idx+1:]
		if !keyDeclared(step, key) {
			return nil, fmt.Errorf("%w: step %q: payload key %q is not declared in writes: (declared keys: %s)",
				ErrParse, step.ID, key, declaredKeysList(step))
		}
		dt := declaredType(step, decls, key)
		if dt == "" {
			return nil, fmt.Errorf("%w: step %q: key %q has no declared state type in writes: or state:", ErrParse, step.ID, key)
		}
		coerced, err := coerceFromPairs(step.ID, key, val, dt)
		if err != nil {
			return nil, err
		}
		writes[key] = coerced
	}
	return writes, nil
}

func badType(stepID, key, declType string, v any) error {
	return fmt.Errorf("%w: step %q: key %q: value %v does not fit declared type %q — "+
		"either print a %s-shaped value for %q from the step, or change %q's declared type in state:/writes:",
		ErrParse, stepID, key, v, declType, declType, key, key)
}

// coerceFromJSON coerces a decoded JSON value (json.Number/bool/string/
// map[string]any/[]any/nil) to declType. A JSON string is accepted for a
// numeric or boolean declType too (e.g. "3" for an integer key), since the
// payload's own JSON-ness is not the author's contract — the declared
// state: type is.
func coerceFromJSON(stepID, key string, v any, declType string) (any, error) {
	switch declType {
	case "integer":
		switch x := v.(type) {
		case json.Number:
			i, err := x.Int64()
			if err != nil {
				return nil, badType(stepID, key, declType, v)
			}
			return i, nil
		case string:
			i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			if err != nil {
				return nil, badType(stepID, key, declType, v)
			}
			return i, nil
		default:
			return nil, badType(stepID, key, declType, v)
		}
	case "number":
		switch x := v.(type) {
		case json.Number:
			f, err := x.Float64()
			if err != nil {
				return nil, badType(stepID, key, declType, v)
			}
			return f, nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return nil, badType(stepID, key, declType, v)
			}
			return f, nil
		default:
			return nil, badType(stepID, key, declType, v)
		}
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(x))
			if err != nil {
				return nil, badType(stepID, key, declType, v)
			}
			return b, nil
		default:
			return nil, badType(stepID, key, declType, v)
		}
	case "string":
		switch x := v.(type) {
		case string:
			return EscapeC0(x), nil
		case json.Number:
			// A JSON number is an unambiguous textual value; coerce it to
			// its own text rather than reject it just because the author's
			// script emitted an unquoted number for a string-typed key
			// (Finding F6).
			return EscapeC0(x.String()), nil
		default:
			return nil, badType(stepID, key, declType, v)
		}
	case "json":
		return escapeValue(v), nil
	default:
		return nil, fmt.Errorf("%w: step %q: key %q declares unknown state type %q", ErrParse, stepID, key, declType)
	}
}

// coerceFromPairs coerces a raw pairs value string to declType.
func coerceFromPairs(stepID, key, raw, declType string) (any, error) {
	switch declType {
	case "integer":
		i, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, badType(stepID, key, declType, raw)
		}
		return i, nil
	case "number":
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, badType(stepID, key, declType, raw)
		}
		return f, nil
	case "boolean":
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return nil, badType(stepID, key, declType, raw)
		}
		return b, nil
	case "string":
		return EscapeC0(raw), nil
	case "json":
		sanitized := sanitizeJSONStrings(raw)
		dec := json.NewDecoder(strings.NewReader(sanitized))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, badType(stepID, key, declType, raw)
		}
		return escapeValue(v), nil
	default:
		return nil, fmt.Errorf("%w: step %q: key %q declares unknown state type %q", ErrParse, stepID, key, declType)
	}
}

// EscapeC0 replaces each C0 control byte (0x00-0x1F) and DEL (0x7F) in s
// with a visible \u00XX escape sequence, passing every other byte through
// unchanged. It operates byte-wise, not rune-wise, so it is total: it
// cannot be handed invalid UTF-8 (a lone 0xFF byte, a broken multi-byte
// sequence) and lose or mangle information, which matters because it is
// also the escape a caller such as the engine's journal applies to raw
// captured stdout before logging it (Finding F3). It is the one boundary
// (design/format-spec.md §B.1) at which control characters arriving through
// a step's stdout are neutralised, so a value can never reintroduce a raw
// control byte into a downstream single-line journal record.
func EscapeC0(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			fmt.Fprintf(&b, `\u%04x`, c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// sanitizeJSONStrings replaces a raw C0 control byte with its \u00XX escape
// only where it appears inside a double-quoted JSON string literal, leaving
// a control byte used as JSON's own insignificant whitespace (a formatting
// TAB or newline between tokens, which `jq`/`printf` routinely emit)
// untouched. A raw control byte inside a JSON string is illegal per RFC
// 8259 and rejected outright by Go's decoder; this lets Parse decode such a
// line instead of misreporting valid, if hostile, stdout as unintelligible
// (Finding F1). It is a syntax-level pre-pass, not a value transform: the
// decoded string values it makes reachable are escaped again, post-decode,
// by EscapeC0/escapeValue.
func sanitizeJSONStrings(s string) string {
	var b strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			case c < 0x20:
				fmt.Fprintf(&b, `\u%04x`, c)
				continue
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = true
		}
		b.WriteByte(c)
	}
	return b.String()
}

// escapeValue recursively applies EscapeC0 to every string found within a
// decoded JSON value (for the "json" declared state type) — including map
// keys, not just values (Finding F2) — leaving json.Number, bool and nil
// untouched.
func escapeValue(v any) any {
	switch x := v.(type) {
	case string:
		return EscapeC0(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[EscapeC0(k)] = escapeValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = escapeValue(val)
		}
		return out
	default:
		return v
	}
}
