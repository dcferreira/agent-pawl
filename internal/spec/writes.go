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
	// IsTyped is true when writes: was authored as a map.
	IsTyped bool
}

type writesEntry struct {
	Type string `yaml:"type"`
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
			if err := v.Decode(&entry); err != nil {
				return fmt.Errorf("writes[%s]: %w", key, err)
			}
			w.Keys = append(w.Keys, key)
			w.Types[key] = entry.Type
		}
		return nil
	default:
		return fmt.Errorf("writes: expected a list or a map, got %v", node.Tag)
	}
}
