package spec

import (
	"fmt"
	"sort"
	"strings"
)

// ReservedEnvNames lists the environment variables pawl itself reads (plus
// PAWL_WRAPPER_ACTIVE, which the plugin's bin/pawl launcher reads); a
// state:/args: key whose PAWL_<KEY> export would shadow one is rejected
// (rule 38).
var ReservedEnvNames = []string{"PAWL_ENFORCEMENT", "PAWL_STATE_DIR", "PAWL_WRAPPER_ACTIVE"}

// pseudoKeyNames are the engine pseudo-keys (lower-case). A state:/args: key
// equal to one of them, in any case, would export as the same PAWL_* variable
// the engine sets itself (rule 38).
var pseudoKeyNames = []string{"run_id", "step", "attempt", "visits", "last_error", "blocked_reason", "item", "item_index"}

// declaredKey is one state:/args: key with the section it came from.
type declaredKey struct{ section, key string }

// declaredKeys returns every state: and args: key, sorted by key then
// section, so every rule below reports in a deterministic order.
func declaredKeys(w *Workflow) []declaredKey {
	var out []declaredKey
	for k := range w.State {
		out = append(out, declaredKey{"state", k})
	}
	for k := range w.Args {
		out = append(out, declaredKey{"args", k})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].key != out[j].key {
			return out[i].key < out[j].key
		}
		return out[i].section < out[j].section
	})
	return out
}

// checkRule37 implements §H rule 37: done and blocked name the implicit
// terminals, so no step may use them, nor the id of a declared terminal:.
// A declared terminal: done/blocked remains valid (it overrides the implicit
// one's message and status).
func checkRule37(w *Workflow, errs *[]string) {
	for _, s := range w.Steps {
		switch {
		case s.ID == "done" || s.ID == "blocked":
			*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
				"rule 37: step id %q is reserved for the implicit terminal; rename the step", s.ID)))
		default:
			if _, ok := w.Terminal[s.ID]; ok {
				*errs = append(*errs, stepErr(w, s.ID, fmt.Sprintf(
					"rule 37: step id %q collides with terminal: %q; rename one", s.ID, s.ID)))
			}
		}
	}
}

// checkRule38 implements §H rule 38: a state:/args: key may not be an engine
// pseudo-key name (case-insensitively), start with an underscore, or export
// as an environment variable pawl itself reads (ReservedEnvNames).
func checkRule38(w *Workflow, errs *[]string) {
	for _, d := range declaredKeys(w) {
		lower := strings.ToLower(d.key)
		upper := "PAWL_" + strings.ToUpper(d.key)
		pseudo := false
		for _, p := range pseudoKeyNames {
			if lower == p {
				pseudo = true
			}
		}
		switch {
		case pseudo:
			*errs = append(*errs, fileErr(w, fmt.Sprintf(
				"rule 38: %s: %q is an engine pseudo-key name (exported as %s); rename it", d.section, d.key, upper)))
		case strings.HasPrefix(d.key, "_"):
			*errs = append(*errs, fileErr(w, fmt.Sprintf(
				"rule 38: %s: %q starts with \"_\"; keys exported as PAWL__* are reserved for pawl's own future environment variables; rename it", d.section, d.key)))
		default:
			for _, r := range ReservedEnvNames {
				if upper == r {
					*errs = append(*errs, fileErr(w, fmt.Sprintf(
						"rule 38: %s: %q would export as %s, an environment variable pawl itself reads; rename it", d.section, d.key, r)))
				}
			}
		}
	}
}

// checkRule39 implements §H rule 39: state: and args: share one namespace, so
// the same key in both, or two keys equal case-insensitively (both export as
// the same PAWL_* variable), is an error.
func checkRule39(w *Workflow, errs *[]string) {
	keys := declaredKeys(w)
	for i := 1; i < len(keys); i++ {
		if keys[i].key == keys[i-1].key {
			*errs = append(*errs, fileErr(w, fmt.Sprintf(
				"rule 39: %q is declared in both args: and state:; keys share one namespace; rename one", keys[i].key)))
		}
	}
	// One representative (first by sort order) per distinct exact key, then
	// compare each against the first of its case-insensitive group.
	byUpper := map[string][]declaredKey{}
	var order []string
	for i, d := range keys {
		if i > 0 && keys[i-1].key == d.key {
			continue
		}
		u := strings.ToUpper(d.key)
		if _, ok := byUpper[u]; !ok {
			order = append(order, u)
		}
		byUpper[u] = append(byUpper[u], d)
	}
	sort.Strings(order)
	for _, u := range order {
		g := byUpper[u]
		for _, other := range g[1:] {
			*errs = append(*errs, fileErr(w, fmt.Sprintf(
				"rule 39: %s: %q and %s: %q both export as PAWL_%s; rename one", g[0].section, g[0].key, other.section, other.key, u)))
		}
	}
}
