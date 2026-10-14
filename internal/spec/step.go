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
	ID   string `json:"id"`
	Kind string `json:"kind"`

	// deterministic
	Run   string `json:"run"`
	Emits string `json:"emits"`

	// agentic
	Description  string         `json:"description"`
	Context      []ContextEntry `json:"context"`
	SubagentArgs any            `json:"subagent_args"`

	// wait
	Poll  string `json:"poll"`
	Every string `json:"every"`

	// wait, human
	Timeout string `json:"timeout"`

	// human
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	OptionsFrom string   `json:"options_from"`
	Multi       bool     `json:"multi"`

	// all
	Writes        Writes         `json:"writes"`
	Postcondition *Postcondition `json:"postcondition"`

	// AttemptsRaw is the value as authored, nil if omitted; Attempts is the
	// defaulted value that engine code should use, set by applyDefaults
	// (Load calls it; Validate calls it defensively too).
	AttemptsRaw *int   `json:"attempts_raw"`
	Attempts    int    `json:"attempts"`
	AttemptKey  string `json:"attempt_key"`

	// MaxVisitsRaw is the value as authored, nil if omitted; MaxVisits is
	// the defaulted value that engine code should use, set the same way.
	MaxVisitsRaw *int `json:"max_visits_raw"`
	MaxVisits    int  `json:"max_visits"`

	// Retry is the step's retry: block (design/format-spec.md §D, §B.16),
	// nil when not declared. Validate (rule 19) restricts it to
	// deterministic and wait steps and checks its fields; the engine
	// (internal/engine) reads it to retry a hard-failed body before
	// resolving any outcome.
	Retry *RetryDecl `json:"retry"`

	Catch []CatchRule `json:"catch"`

	Next     string            `json:"next"`
	Outcomes map[string]string `json:"outcomes"`

	// parallel
	Branches []string `json:"branches"`
	// Foreach is the alternative to Branches on kind: parallel
	// (design/format-spec.md §B.15): nil when not declared.
	Foreach *Foreach `json:"foreach"`

	// UnknownFields lists, sorted, any YAML key under this step that is not
	// part of the authoring format — reported by Validate rather than
	// silently ignored.
	UnknownFields []string `json:"unknown_fields"`

	// Fields lists, sorted, every YAML key the author actually wrote under
	// this step, known or not (rule 30 reads it: defaulted fields such as
	// emits:/every: must not count as authored). It is populated only by
	// UnmarshalYAML; a Step built as a Go struct literal has no Fields, so
	// rule 30 sees no authored keys on it and reports nothing.
	Fields []string `json:"-"`
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

	Retry *RetryDecl `yaml:"retry"`

	Catch []CatchRule `yaml:"catch"`

	Next     string            `yaml:"next"`
	Outcomes map[string]string `yaml:"outcomes"`

	Branches []string `yaml:"branches"`
	Foreach  *Foreach `yaml:"foreach"`
}

// commonStepFields are the step-level keys every kind accepts (rule 30).
// attempt_key is common because the engine's beginAttempt uses it as the
// ${attempt} counter key on every kind; attempts: stays deterministic/agentic.
var commonStepFields = []string{"id", "kind", "next", "outcomes", "catch", "max_visits", "attempt_key"}

// kindFields is the single source of truth for which authored step keys each
// kind accepts (design/format-spec.md §H rule 30). The keys in commonStepFields
// are accepted by every kind and are not repeated here. stepKnownFields (the
// set of keys the format knows at all, used for unknown-key detection) is
// derived from this table.
var kindFields = map[string][]string{
	"deterministic": {"run", "emits", "writes", "postcondition", "soft", "attempts", "retry"},
	"agentic":       {"description", "context", "subagent_args", "writes", "postcondition", "soft", "attempts"},
	"wait":          {"poll", "every", "timeout", "emits", "writes", "postcondition", "soft", "retry"},
	"human":         {"question", "options", "options_from", "multi", "timeout", "writes"},
	"parallel":      {"branches", "foreach"},
}

// stepKnownFields is the set of step-level YAML keys this build understands:
// the union of commonStepFields and every kind's kindFields entry.
var stepKnownFields = deriveStepKnownFields()

func deriveStepKnownFields() map[string]bool {
	m := map[string]bool{}
	for _, k := range commonStepFields {
		m[k] = true
	}
	for _, fields := range kindFields {
		for _, k := range fields {
			m[k] = true
		}
	}
	return m
}

// kindAccepts reports whether kind accepts the authored key.
func kindAccepts(kind, key string) bool {
	for _, k := range commonStepFields {
		if k == key {
			return true
		}
	}
	for _, k := range kindFields[kind] {
		if k == key {
			return true
		}
	}
	return false
}

// kindsAccepting lists, in a fixed kind order, the kinds that accept key.
func kindsAccepting(key string) []string {
	var out []string
	for _, kind := range []string{"deterministic", "agentic", "wait", "human", "parallel"} {
		if kindAccepts(kind, key) {
			out = append(out, kind)
		}
	}
	return out
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

	var unknown, fields []string
	for k := range raw {
		fields = append(fields, k)
		if !stepKnownFields[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	sort.Strings(fields)

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
		Foreach:       sh.Foreach,
		UnknownFields: unknown,
		Fields:        fields,
	}

	if s.Postcondition != nil && sh.Soft && !s.Postcondition.SoftSet {
		s.Postcondition.Soft = true
	}
	return nil
}
