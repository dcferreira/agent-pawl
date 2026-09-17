package spec

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Postcondition is the command or predicate the engine evaluates before
// leaving a step (design/format-spec.md §D). It is polymorphic in YAML: a
// bare string is equivalent to {command: <string>}; a map takes exactly one
// of command/all_set/equals, plus an optional soft.
//
// HasCommand/HasAllSet/HasEquals record which alternative was authored, so
// Validate can enforce "exactly one" (rule 14) with a message naming what
// was actually found. UnknownKeys records any map key other than
// command/all_set/equals/soft, for the same rule.
type Postcondition struct {
	Command string
	AllSet  []string
	Equals  map[string]any

	HasCommand bool
	HasAllSet  bool
	HasEquals  bool

	// Soft is set either by a top-level soft: key nested in the map form.
	// Load merges the step's own top-level soft: field into this.
	Soft    bool
	SoftSet bool

	UnknownKeys []string
}

// UnmarshalYAML implements the postcondition: polymorphism. It does not
// itself enforce "exactly one of command/all_set/equals" or reject unknown
// keys with a user-facing message: that is validator rule 14, which has the
// file and step id needed to phrase the fix.
//
// The map form is decoded via node.Decode(&map[string]yaml.Node{}), which
// resolves YAML merge keys ("<<: *anchor") before we ever look at the key
// set — a raw node.Content scan would see "<<" itself as an unknown key and
// would never see the merged-in keys at all (Finding F3).
func (p *Postcondition) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var s string
		if err := node.Decode(&s); err != nil {
			return fmt.Errorf("postcondition: %w", err)
		}
		p.Command = s
		p.HasCommand = true
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("postcondition: expected a string or a map, got %v", node.Tag)
	}

	raw := map[string]yaml.Node{}
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("postcondition: %w", err)
	}

	if v, ok := raw["command"]; ok {
		var s string
		if err := v.Decode(&s); err != nil {
			return fmt.Errorf("postcondition.command: %w", err)
		}
		p.Command = s
		p.HasCommand = true
	}
	if v, ok := raw["all_set"]; ok {
		var l []string
		if err := v.Decode(&l); err != nil {
			return fmt.Errorf("postcondition.all_set: %w", err)
		}
		p.AllSet = l
		p.HasAllSet = true
	}
	if v, ok := raw["equals"]; ok {
		var m map[string]any
		if err := v.Decode(&m); err != nil {
			return fmt.Errorf("postcondition.equals: %w", err)
		}
		p.Equals = m
		p.HasEquals = true
	}
	if v, ok := raw["soft"]; ok {
		var b bool
		if err := v.Decode(&b); err != nil {
			return fmt.Errorf("postcondition.soft: %w", err)
		}
		p.Soft = b
		p.SoftSet = true
	}

	known := map[string]bool{"command": true, "all_set": true, "equals": true, "soft": true}
	for k := range raw {
		if !known[k] {
			p.UnknownKeys = append(p.UnknownKeys, k)
		}
	}
	sort.Strings(p.UnknownKeys)
	return nil
}

// altCount returns how many of command/all_set/equals were authored.
func (p *Postcondition) altCount() int {
	n := 0
	if p.HasCommand {
		n++
	}
	if p.HasAllSet {
		n++
	}
	if p.HasEquals {
		n++
	}
	return n
}
