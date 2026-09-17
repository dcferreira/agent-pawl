package spec

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ContextEntry is one entry of an agentic step's context: list
// (design/format-spec.md §D). A plain scalar names a file, read relative to
// the workflow file; a "!cmd" -tagged scalar is a command whose output is
// gathered instead — ${key} substitution resolves before the command runs
// (§B.2). The YAML tag is the only thing that distinguishes the two, so it
// must be captured here rather than lost to a plain []string decode.
type ContextEntry struct {
	IsCmd bool
	Value string
}

// UnmarshalYAML records whether node carried the custom "!cmd" tag before
// decoding its scalar value, the same way Postcondition and Writes capture
// their own YAML polymorphism.
func (c *ContextEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("context entry: expected a string (optionally tagged !cmd), got %v", node.Tag)
	}
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("context entry: %w", err)
	}
	c.Value = s
	c.IsCmd = node.Tag == "!cmd"
	return nil
}
