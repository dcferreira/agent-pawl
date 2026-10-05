package spec

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// DefaultForeachMaxItems is the cap on a foreach: fan-out's list length when
// max_items: is omitted (design/format-spec.md §B.15, §D).
const DefaultForeachMaxItems = 20

// ForeachItemKeys are the engine pseudo-keys readable only in a foreach:
// body's renderable fields: the current item and its zero-based index. They
// are deliberately not in PseudoKeys, which is readable in every context
// (design/format-spec.md §B.2, §B.15); rule 27 scopes and reserves them.
var ForeachItemKeys = []string{"item", "item_index"}

func isForeachItemKey(key string) bool {
	for _, k := range ForeachItemKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Foreach is a kind: parallel step's foreach: block (design/format-spec.md
// §B.15, §D): an alternative to branches: that runs one body step once per
// item of a json state key's array, then joins with success, partial or
// failure.
//
// MaxItems is the value as authored, 0 when omitted; use MaxItemsOrDefault
// for the effective cap. A non-positive authored value is reported by
// Validate rather than silently defaulted, so 0 and "omitted" are
// deliberately indistinguishable here (an explicit max_items: 0 is also
// rejected at validate time through MaxItemsSet).
type Foreach struct {
	Over     string `yaml:"over"`      // a declared json state key holding an array
	Body     string `yaml:"body"`      // id of the single step run once per item
	Collect  string `yaml:"collect"`   // a declared json state key receiving per-item results
	MaxItems int    `yaml:"max_items"` // default 20 when 0/omitted

	// MaxItemsSet records whether max_items: was authored at all, so
	// Validate can reject an explicit 0 or negative rather than treating it
	// as the default. UnknownKeys records any map key outside the four
	// above (mirrors RetryDecl.UnknownKeys).
	MaxItemsSet bool     `yaml:"-"`
	UnknownKeys []string `yaml:"-"`
}

// MaxItemsOrDefault returns the effective list-length cap: MaxItems, or
// DefaultForeachMaxItems when it is 0.
func (f *Foreach) MaxItemsOrDefault() int {
	if f.MaxItems == 0 {
		return DefaultForeachMaxItems
	}
	return f.MaxItems
}

// foreachShadow mirrors Foreach's YAML-facing fields for the one decode pass
// UnmarshalYAML performs.
type foreachShadow struct {
	Over     string `yaml:"over"`
	Body     string `yaml:"body"`
	Collect  string `yaml:"collect"`
	MaxItems *int   `yaml:"max_items"`
}

// UnmarshalYAML decodes node into a map first (resolving YAML merge keys,
// mirrored from RetryDecl) so unknown-key detection never trips on "<<".
func (f *Foreach) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("foreach: expected a map, got %v", node.Tag)
	}

	raw := map[string]yaml.Node{}
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("foreach: %w", err)
	}

	var sh foreachShadow
	if err := node.Decode(&sh); err != nil {
		return fmt.Errorf("foreach: %w", err)
	}

	known := map[string]bool{"over": true, "body": true, "collect": true, "max_items": true}
	var unknown []string
	for k := range raw {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)

	*f = Foreach{Over: sh.Over, Body: sh.Body, Collect: sh.Collect, UnknownKeys: unknown}
	if sh.MaxItems != nil {
		f.MaxItems = *sh.MaxItems
		f.MaxItemsSet = true
	}
	return nil
}
