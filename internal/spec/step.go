package spec

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Step is one node of the workflow graph. Not every field applies to every
// Kind; design/format-spec.md §D and §C say which.
//
// Step has a custom UnmarshalYAML for two reasons: it merges a step-level
// soft: (design/format-spec.md §D, §E) into Postcondition.Soft at parse
// time, so Postcondition.Soft is the only place later code ever needs to
// read whether a step advances on a soft check; and it records any field
// name this build does not recognise (a likely typo, e.g. nxt: for next:)
// in UnknownFields rather than silently dropping it (Global Constraint 5).
type Step struct {
	ID   string
	Kind string

	// deterministic
	Run   string
	Emits string

	// agentic
	Description  string
	Context      []ContextEntry
	SubagentArgs any

	// wait
	Poll  string
	Every string

	// wait, human
	Timeout string

	// human
	Question    string
	Options     []string
	OptionsFrom string
	Multi       bool

	// all
	Writes        Writes
	Postcondition *Postcondition

	// AttemptsRaw is the value as authored, nil if omitted; Attempts is the
	// defaulted value that engine code should use, set by applyDefaults
	// (Load calls it; Validate calls it defensively too).
	AttemptsRaw *int
	Attempts    int
	AttemptKey  string

	// MaxVisitsRaw is the value as authored, nil if omitted; MaxVisits is
	// the defaulted value that engine code should use, set the same way.
	MaxVisitsRaw *int
	MaxVisits    int

	// Retry is deferred (Ruling R7): parsed, and rejected by Validate
	// whenever non-nil.
	Retry any

	Catch []CatchRule

	Next     string
	Outcomes map[string]string

	// parallel
	Branches []string

	// UnknownFields lists, sorted, any YAML key under this step that is not
	// part of the authoring format — reported by Validate rather than
	// silently ignored.
	UnknownFields []string
}

// stepShadow mirrors Step's YAML-facing fields for the one decode pass
// Step.UnmarshalYAML performs. It exists because Step itself must implement
// yaml.Unmarshaler to merge soft: and detect unknown fields, and a type
// cannot use struct-tag reflection decoding for itself once it does.
type stepShadow struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`

	Run   string `yaml:"run"`
	Emits string `yaml:"emits"`

	Description  string         `yaml:"description"`
	Context      []ContextEntry `yaml:"context"`
	SubagentArgs any            `yaml:"subagent_args"`

	Poll  string `yaml:"poll"`
	Every string `yaml:"every"`

	Timeout string `yaml:"timeout"`

	Question    string   `yaml:"question"`
	Options     []string `yaml:"options"`
	OptionsFrom string   `yaml:"options_from"`
	Multi       bool     `yaml:"multi"`

	Writes        Writes         `yaml:"writes"`
	Postcondition *Postcondition `yaml:"postcondition"`
	Soft          bool           `yaml:"soft"`

	AttemptsRaw *int   `yaml:"attempts"`
	AttemptKey  string `yaml:"attempt_key"`

	MaxVisitsRaw *int `yaml:"max_visits"`

	Retry any `yaml:"retry"`

	Catch []CatchRule `yaml:"catch"`

	Next     string            `yaml:"next"`
	Outcomes map[string]string `yaml:"outcomes"`

	Branches []string `yaml:"branches"`
}

// stepKnownFields is the set of step-level YAML keys this build understands.
var stepKnownFields = map[string]bool{
	"id": true, "kind": true, "run": true, "emits": true,
	"description": true, "context": true, "subagent_args": true,
	"poll": true, "every": true, "timeout": true,
	"question": true, "options": true, "options_from": true, "multi": true,
	"writes": true, "postcondition": true, "soft": true,
	"attempts": true, "attempt_key": true, "max_visits": true,
	"retry": true, "catch": true, "next": true, "outcomes": true,
	"branches": true,
}

// UnmarshalYAML decodes node into a map first (which resolves YAML merge
// keys, per Finding F3) so unknown-field detection never trips on "<<", then
// decodes it again into stepShadow for the typed fields.
func (s *Step) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("step: expected a map, got %v", node.Tag)
	}

	raw := map[string]yaml.Node{}
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("step: %w", err)
	}
	var id string
	if v, ok := raw["id"]; ok {
		_ = v.Decode(&id)
	}

	var sh stepShadow
	if err := node.Decode(&sh); err != nil {
		if id != "" {
			return fmt.Errorf("step %q: %w", id, err)
		}
		return fmt.Errorf("step: %w", err)
	}

	var unknown []string
	for k := range raw {
		if !stepKnownFields[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)

	*s = Step{
		ID:            sh.ID,
		Kind:          sh.Kind,
		Run:           sh.Run,
		Emits:         sh.Emits,
		Description:   sh.Description,
		Context:       sh.Context,
		SubagentArgs:  sh.SubagentArgs,
		Poll:          sh.Poll,
		Every:         sh.Every,
		Timeout:       sh.Timeout,
		Question:      sh.Question,
		Options:       sh.Options,
		OptionsFrom:   sh.OptionsFrom,
		Multi:         sh.Multi,
		Writes:        sh.Writes,
		Postcondition: sh.Postcondition,
		AttemptsRaw:   sh.AttemptsRaw,
		AttemptKey:    sh.AttemptKey,
		MaxVisitsRaw:  sh.MaxVisitsRaw,
		Retry:         sh.Retry,
		Catch:         sh.Catch,
		Next:          sh.Next,
		Outcomes:      sh.Outcomes,
		Branches:      sh.Branches,
		UnknownFields: unknown,
	}

	if s.Postcondition != nil && sh.Soft && !s.Postcondition.SoftSet {
		s.Postcondition.Soft = true
	}
	return nil
}
