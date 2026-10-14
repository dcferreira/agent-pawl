package spec

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// validTypes is the closed set of state:/args:/writes: types (rule 32).
var validTypes = []string{"string", "integer", "number", "boolean", "json"}

func isValidType(t string) bool {
	for _, v := range validTypes {
		if v == t {
			return true
		}
	}
	return false
}

const validTypesList = "string, integer, number, boolean, json"

// checkRule30 implements §H rule 30: a step may carry only the authored keys
// its kind accepts (kindFields). It works on Step.Fields, the keys the author
// actually wrote, so defaulted fields never trip it. Unknown keys are left to
// checkUnknownFields, and a step with an unknown kind to checkKindSupport.
func checkRule30(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if _, ok := kindFields[s.Kind]; !ok {
			continue
		}
		var bad []string
		for _, f := range s.Fields {
			if stepKnownFields[f] && !kindAccepts(s.Kind, f) {
				bad = append(bad, f)
			}
		}
		if len(bad) == 0 {
			continue
		}
		// Group the rejected fields by the kinds that own them, in field
		// order, so the fix names where each field belongs.
		var groups []string
		seen := map[string]bool{}
		for _, f := range bad {
			owners := strings.Join(kindsAccepting(f), ", ")
			if seen[owners] {
				continue
			}
			seen[owners] = true
			var sameOwner []string
			for _, g := range bad {
				if strings.Join(kindsAccepting(g), ", ") == owners {
					sameOwner = append(sameOwner, g+":")
				}
			}
			verb := "belongs"
			if len(sameOwner) > 1 {
				verb = "belong"
			}
			groups = append(groups, fmt.Sprintf("%s %s to kind: %s", strings.Join(sameOwner, ", "), verb, owners))
		}
		labelled := make([]string, len(bad))
		for i, f := range bad {
			labelled[i] = f + ":"
		}
		pronoun := "it"
		if len(bad) > 1 {
			pronoun = "them"
		}
		*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
			"rule 30: kind: %s does not accept %s (%s); remove %s", s.Kind, strings.Join(labelled, ", "), strings.Join(groups, "; "), pronoun)))
	}
}

// checkRule31 implements §H rule 31: soft: is a modifier of a postcondition,
// so authoring it without one is an error (it used to be silently dropped).
// Only checked where the kind accepts soft: at all; elsewhere rule 30 fires.
func checkRule31(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		if !kindAccepts(s.Kind, "soft") || s.Postcondition != nil {
			continue
		}
		for _, f := range s.Fields {
			if f == "soft" {
				*errs = append(*errs, stepErr(w, s.ID,
					"rule 31: soft: requires postcondition:; add a postcondition:, or remove soft: (it has nothing to soften)"))
				break
			}
		}
	}
}

// typeProblem returns a rule-32 fault description for a declared type, or ""
// when it is valid. what is the "state: \"x\"" style subject.
func typeProblem(what, typ string, allowMissing bool) string {
	switch {
	case typ == "" && allowMissing:
		return ""
	case typ == "":
		return fmt.Sprintf("%s has no type:; add type: string|integer|number|boolean|json", what)
	case !isValidType(typ):
		msg := fmt.Sprintf("%s: type: %q is not one of %s", what, typ, validTypesList)
		if sug := closestField(typ, validTypes); sug != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", sug)
		}
		return msg
	}
	return ""
}

// checkRule32 implements §H rule 32: every state:/args: entry and every typed
// writes: entry has a type: from the closed enum.
func checkRule32(w *Workflow, errs *[]string) {
	for _, k := range sortedKeys(w.State) {
		if msg := typeProblem(fmt.Sprintf("state: %q", k), w.State[k].Type, false); msg != "" {
			*errs = append(*errs, fileErr(w, "rule 32: "+msg))
		}
	}
	for _, k := range sortedKeys(w.Args) {
		if msg := typeProblem(fmt.Sprintf("args: %q", k), w.Args[k].Type, false); msg != "" {
			*errs = append(*errs, fileErr(w, "rule 32: "+msg))
		}
	}
	for _, s := range w.Steps {
		if !s.Writes.IsTyped {
			continue
		}
		for _, k := range s.Writes.Keys {
			// A missing type on an agentic step is reported by
			// checkAgenticWrites; do not double-report it.
			if msg := typeProblem(fmt.Sprintf("writes: %q", k), s.Writes.Types[k], s.Kind == "agentic"); msg != "" {
				*errs = append(*errs, stepErr(w, s.ID, "rule 32: "+msg))
			}
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// defaultKind names a decoded YAML default's Go-side kind for messages.
func defaultKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case int, int64, uint64:
		return "an integer"
	case float64:
		return "a float"
	case bool:
		return "a boolean"
	case []any:
		return "a list"
	case map[string]any:
		return "a map"
	default:
		return fmt.Sprintf("a %T", v)
	}
}

// defaultMatches reports whether a decoded YAML default fits a declared type.
func defaultMatches(typ string, v any) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		switch v.(type) {
		case int, int64, uint64:
			return true
		}
		return false
	case "number":
		switch v.(type) {
		case int, int64, uint64, float64:
			return true
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "json":
		return true
	}
	return true
}

// checkRule33 implements §H rule 33: default: must match type:, as YAML
// decodes it. Skipped when the type itself is invalid or missing (rule 32).
func checkRule33(w *Workflow, errs *[]string) {
	check := func(what, typ string, set bool, def *any) {
		if !isValidType(typ) || !set {
			return
		}
		var v any
		if def != nil {
			v = *def
		}
		if defaultMatches(typ, v) {
			return
		}
		shown := fmt.Sprintf("%v", v)
		if s, ok := v.(string); ok {
			shown = fmt.Sprintf("%q", s)
		}
		fix := "fix the default or change the type"
		if typ == "string" {
			fix = fmt.Sprintf("quote it (%q) or change the type", fmt.Sprintf("%v", v))
		}
		*errs = append(*errs, fileErr(w, fmt.Sprintf(
			"rule 33: %s: default: %s is %s, but type: is %s; %s", what, shown, defaultKind(v), typ, fix)))
	}
	for _, k := range sortedKeys(w.State) {
		d := w.State[k]
		check(fmt.Sprintf("state: %q", k), d.Type, d.DefaultSet || d.Default != nil, d.Default)
	}
	for _, k := range sortedKeys(w.Args) {
		d := w.Args[k]
		check(fmt.Sprintf("args: %q", k), d.Type, d.DefaultSet || d.Default != nil, d.Default)
	}
}

// checkRule34 implements §H rule 34: a catch[] entry has exactly the keys on:
// and next:. A missing next: is reported by rule 1, a missing on: here.
func checkRule34(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		for i, c := range s.Catch {
			for _, k := range c.UnknownKeys {
				msg := fmt.Sprintf("rule 34: catch[%d] has unknown key %q", i, k)
				if k != "on" && k != "next" {
					if sug := closestField(k, []string{"on", "next"}); sug != "" {
						msg += fmt.Sprintf(" (did you mean %q?)", sug)
					}
				}
				*errs = append(*errs, stepErr(w, s.ID, msg+"; use only on: and next:"))
			}
			if c.On == "" {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 34: catch[%d].on: is required; add on: <outcome kind> (e.g. failure)", i)))
			}
		}
	}
}

// catchableOutcomes is the reserved, non-success outcomes a catch: entry of
// the step's kind may name (rule 35), plus the label used in messages.
func catchableOutcomes(s Step) (label string, set []string) {
	switch s.Kind {
	case "deterministic", "agentic":
		return "kind: " + s.Kind, []string{"failure", "exhausted"}
	case "wait", "human":
		return "kind: " + s.Kind, []string{"failure", "timeout", "exhausted"}
	case "parallel":
		if s.Foreach != nil {
			return "kind: parallel with foreach", []string{"failure", "partial", "exhausted"}
		}
		return "kind: parallel with branches", []string{"failure", "exhausted"}
	}
	return "", nil
}

// checkRule35 implements §H rule 35: catch[].on must be an outcome the kind
// can catch, and an entry that can never fire (a duplicate on:, or one already
// routed by outcomes:) is an error.
func checkRule35(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		label, allowed := catchableOutcomes(s)
		if allowed == nil {
			continue
		}
		first := map[string]int{}
		for i, c := range s.Catch {
			if c.On == "" {
				continue // rule 34
			}
			ok := false
			for _, a := range allowed {
				if a == c.On {
					ok = true
				}
			}
			if !ok {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 35: catch[%d].on: %q is not an outcome a %s step can catch (%s); route author-named outcomes under outcomes:",
					i, c.On, label, strings.Join(allowed, ", "))))
				continue
			}
			if j, dup := first[c.On]; dup {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 35: catch[%d].on: %q duplicates catch[%d]; the later entry can never fire, remove it", i, c.On, j)))
				continue
			}
			first[c.On] = i
			if _, routed := s.Outcomes[c.On]; routed {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 35: catch[%d].on: %q is already routed by outcomes:, which takes precedence; remove one", i, c.On)))
			}
		}
	}
}

// checkRule36 implements §H rule 36: a wait or human timeout:, and a wait
// every:, must parse as a positive Go duration.
func checkRule36(w *Workflow, errs *[]string) {
	check := func(s Step, field, val, example string) {
		if val == "" {
			return
		}
		d, err := time.ParseDuration(val)
		if err != nil {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 36: %s: %q is not a valid duration; use Go duration syntax, e.g. %s", field, val, example)))
		} else if d <= 0 {
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 36: %s: %q must be greater than zero", field, val)))
		}
	}
	for _, s := range w.Steps {
		switch s.Kind {
		case "wait":
			check(s, "every", s.Every, `"60s", "5m"`)
			check(s, "timeout", s.Timeout, `"5m", "1h", "24h"`)
		case "human":
			check(s, "timeout", s.Timeout, `"5m", "1h", "24h"`)
		}
	}
}
