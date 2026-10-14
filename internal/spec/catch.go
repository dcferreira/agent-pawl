package spec

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// catchShadow mirrors CatchRule's YAML-facing fields for the one decode pass
// UnmarshalYAML performs.
type catchShadow struct {
	On   string `yaml:"on"`
	Next string `yaml:"next"`
}

// UnmarshalYAML decodes node into a map first (resolving YAML merge keys,
// mirrored from RetryDecl) so unknown-key detection never trips on "<<", then
// into catchShadow for the typed fields. An unknown key is recorded in
// UnknownKeys for Validate (rule 34) rather than failing the load.
func (c *CatchRule) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("catch: expected a map {on: <outcome>, next: <target>}, got %v", node.Tag)
	}
	raw := map[string]yaml.Node{}
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("catch: %w", err)
	}
	var sh catchShadow
	if err := node.Decode(&sh); err != nil {
		return fmt.Errorf("catch: %w", err)
	}
	var unknown []string
	for k := range raw {
		if k != "on" && k != "next" {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	*c = CatchRule{On: sh.On, Next: sh.Next, UnknownKeys: unknown}
	return nil
}
