package spec

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// RetryDecl is a step's retry: block (design/format-spec.md §D, §B.16):
// retries the step's *body* on a hard failure, before any outcome is
// resolved — distinct from attempts:, which re-runs a step on a
// postcondition failure. A nil *RetryDecl on Step means "no retry:
// declared"; Validate rejects retry: on any kind other than deterministic
// or wait (rule 19).
//
// MaxAttemptsRaw/BackoffRaw are the values as authored, kept as strings/ints
// exactly as YAML gave them so Validate can report what was actually written
// (a negative integer, an unparseable duration) rather than a
// silently-defaulted value. There is no default: both fields are required.
type RetryDecl struct {
	MaxAttempts int
	Backoff     string

	// UnknownKeys records any map key other than max_attempts/backoff, for
	// Validate (mirrors Postcondition.UnknownKeys).
	UnknownKeys []string
}

// retryDeclShadow mirrors RetryDecl's YAML-facing fields for the one decode
// pass UnmarshalYAML performs.
type retryDeclShadow struct {
	MaxAttempts int    `yaml:"max_attempts"`
	Backoff     string `yaml:"backoff"`
}

// UnmarshalYAML decodes node into a map first (resolving YAML merge keys,
// per Finding F3, mirrored from Step/Postcondition's own UnmarshalYAML) so
// unknown-field detection never trips on "<<".
func (r *RetryDecl) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("retry: expected a map, got %v", node.Tag)
	}

	raw := map[string]yaml.Node{}
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("retry: %w", err)
	}

	var sh retryDeclShadow
	if err := node.Decode(&sh); err != nil {
		return fmt.Errorf("retry: %w", err)
	}

	known := map[string]bool{"max_attempts": true, "backoff": true}
	var unknown []string
	for k := range raw {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)

	*r = RetryDecl{
		MaxAttempts: sh.MaxAttempts,
		Backoff:     sh.Backoff,
		UnknownKeys: unknown,
	}
	return nil
}
