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
//
// Tag is the raw node.Tag as YAML resolved it: "!!str" for a plain or
// quoted scalar (including one that merely starts with "!", e.g. an author
// wrote "!git diff main...HEAD" in quotes), "!cmd" for the one recognised
// command tag, or an arbitrary custom tag like "!git"/"!bash" for an
// UNQUOTED entry starting with "!" — YAML reads "!git diff main" as the
// custom tag "!git" applied to the scalar "diff main", not as the literal
// string "!git diff main". Validator rule 18 (internal/spec/validate.go)
// needs Tag, not just IsCmd, to catch that unquoted-tag case: without it,
// the tag is silently discarded by node.Decode and the entry is
// indistinguishable from a plain file path named "diff main".
type ContextEntry struct {
	IsCmd bool
	Value string
	Tag   string
}

// UnmarshalYAML records node's tag before decoding its scalar value, the
// same way Postcondition and Writes capture their own YAML polymorphism.
func (c *ContextEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("context entry: expected a string (optionally tagged !cmd), got %v", node.Tag)
	}
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("context entry: %w", err)
	}
	c.Value = s
	c.Tag = node.Tag
	c.IsCmd = node.Tag == "!cmd"
	return nil
}
