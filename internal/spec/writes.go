package spec

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Writes is the normalised form of a step's writes: field, which is
// polymorphic in YAML (design/format-spec.md §D): a plain list of key names,
// or a typed map (required on agentic, where it is the subagent's return
// schema).
type Writes struct {
	// Keys lists the written key names, in declaration order.
	Keys []string
	// Types maps a key to its declared type, present only when writes: was
	// authored as a typed map. Empty/nil when IsTyped is false.
	Types map[string]string
	// MaxLength maps a key to its writes-entry max_length (Unicode code
	// points), present only for keys that declared one. See MaxLengthFor.
	MaxLength map[string]int
	// IsTyped is true when writes: was authored as a map.
	IsTyped bool
}

// writesEntryKnownFields is the set of keys a typed writes: entry may carry.
var writesEntryKnownFields = map[string]bool{"type": true, "max_length": true}

type writesEntry struct {
	Type      string `yaml:"type"`
	MaxLength int    `yaml:"max_length"`
}

// UnmarshalYAML implements the writes: polymorphism.
//
// The map form is decoded via node.Decode(&map[string]yaml.Node{}), which
// resolves YAML merge keys ("<<: *anchor") before we look at the key set —
// a raw node.Content scan would produce a single literal "<<" key and
// silently drop every merged-in key (Finding F3). Because a map has no
// inherent order once merged, keys are sorted for determinism rather than
// preserving authored order.
func (w *Writes) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var keys []string
		if err := node.Decode(&keys); err != nil {
			return fmt.Errorf("writes: %w", err)
		}
		w.Keys = keys
		w.IsTyped = false
		return nil
	case yaml.MappingNode:
		raw := map[string]yaml.Node{}
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("writes: %w", err)
		}
		w.IsTyped = true
		w.Types = map[string]string{}
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			var entry writesEntry
			v := raw[key]
			// node.Decode is not strict (the loader's KnownFields does not
			// propagate), so check the entry's own keys by hand; decoding to
			// a map first keeps merge keys resolving.
			fields := map[string]yaml.Node{}
			if err := v.Decode(&fields); err != nil {
				return fmt.Errorf("writes[%s]: %w", key, err)
			}
			for _, f := range sortedNodeKeys(fields) {
				if !writesEntryKnownFields[f] {
					return fmt.Errorf("writes[%s]: unknown field %q; a typed writes: entry takes only type and max_length", key, f)
				}
			}
			if err := v.Decode(&entry); err != nil {
				return fmt.Errorf("writes[%s]: %w", key, err)
			}
			w.Keys = append(w.Keys, key)
			w.Types[key] = entry.Type
			if _, ok := fields["max_length"]; ok {
				if w.MaxLength == nil {
					w.MaxLength = map[string]int{}
				}
				w.MaxLength[key] = entry.MaxLength
			}
		}
		return nil
	default:
		return fmt.Errorf("writes: expected a list or a map, got %v", node.Tag)
	}
}

// MaxLengthFor is the effective max_length for a write of key by step: the
// tighter (smallest non-zero) of the state: decl's limit and the step's own
// typed writes: limit. 0 means no limit.
func MaxLengthFor(decls map[string]StateDecl, step *Step, key string) int {
	limit := decls[key].MaxLength
	if step != nil {
		if w := step.Writes.MaxLength[key]; w > 0 && (limit <= 0 || w < limit) {
			limit = w
		}
	}
	if limit < 0 {
		return 0
	}
	return limit
}

func sortedNodeKeys(m map[string]yaml.Node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
