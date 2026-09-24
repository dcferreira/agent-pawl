// Package spec defines the authoring format for a pawl workflow file: the Go
// types that mirror design/format-spec.md §D, a YAML loader that applies the
// spec's defaults, and a static validator implementing the design/format-spec.md
// §H subset scoped for this build.
//
// spec depends on internal/render (for render.Keys, used by validator rule 4)
// and on nothing else internal; nothing internal depends on spec's absence of
// a dependency being violated in the other direction.
package spec

// PseudoKeys are the engine-provided keys readable in every ${key} context,
// never declared in state: or args: and never written by a step
// (design/format-spec.md §B.2).
var PseudoKeys = []string{"run_id", "step", "attempt", "visits", "last_error", "blocked_reason"}

func isPseudoKey(key string) bool {
	for _, k := range PseudoKeys {
		if k == key {
			return true
		}
	}
	return false
}

// ArgDecl declares a workflow argument, typed like a state key, bound on the
// command line as key=value and entering run state read-only.
type ArgDecl struct {
	Type     string `yaml:"type"`
	Default  *any   `yaml:"default"`
	Required bool   `yaml:"required"`
}

// StateDecl declares a state key the run may carry.
type StateDecl struct {
	Type      string `yaml:"type"`
	Default   *any   `yaml:"default"`
	MaxLength int    `yaml:"max_length"`
}

// GuardDecl is a guard declaration: {id, match, only_in}
// (design/format-spec.md §B.10, §D). Validate accepts and validates
// guards: (id required+unique, match: required and must compile as a Go
// RE2 regexp, only_in: required — rule 15 checks each entry names a
// declared step; only_in: [] is the explicit spelling for "denied
// everywhere"). Accepted and validated is advisory PreToolUse enforcement
// (internal/guard, internal/hook), not a semantic guarantee, and live only
// while the hooks are installed and firing — see the "guards: N advisory
// (pattern-matched)" / "guards: N declared, NOT enforced" banner lines in
// internal/cli/format.go. OnlyIn is
// nil when only_in: is omitted and non-nil (possibly empty) when authored,
// which the validator uses to tell "omitted" from "only_in: []" apart
// (yaml.v3 already distinguishes the two on decode: verified in
// internal/spec/validate_test.go).
type GuardDecl struct {
	ID     string   `yaml:"id" json:"id"`
	Match  string   `yaml:"match" json:"match"`
	OnlyIn []string `yaml:"only_in" json:"only_in"`
}

// InvariantDecl is an invariant declaration. Invariants are parsed but
// still rejected by Validate in this build (Ruling R8) — only the
// guards:/invariants: split is new; invariants: is unaffected.
type InvariantDecl struct {
	ID      string `yaml:"id"`
	Check   string `yaml:"check"`
	Message string `yaml:"message"`
}

// Terminal names the message shown on reaching a terminal outcome.
type Terminal struct {
	Status  string `yaml:"status"`
	Message string `yaml:"message"`
}

// CatchRule routes a step's exhausted outcome (design/format-spec.md §D).
type CatchRule struct {
	On   string `yaml:"on"`
	Next string `yaml:"next"`
}

// Step is defined in step.go: it has a custom UnmarshalYAML, so it is kept
// out of this file to avoid confusion with the plain-decoded types above.

// Workflow is the parsed and defaulted contents of one workflow file.
type Workflow struct {
	Workflow    string `yaml:"workflow"`
	Description string `yaml:"description"`
	Start       string `yaml:"start"`

	// MaxStepsRaw is the value as authored, nil if omitted; MaxSteps is the
	// defaulted value (200 if MaxStepsRaw is nil) that engine code should
	// use.
	MaxStepsRaw *int `yaml:"max_steps"`
	MaxSteps    int  `yaml:"-"`

	Args       map[string]ArgDecl   `yaml:"args"`
	State      map[string]StateDecl `yaml:"state"`
	Guards     []GuardDecl          `yaml:"guards"`
	Invariants []InvariantDecl      `yaml:"invariants"`
	Steps      []Step               `yaml:"steps"`
	Terminal   map[string]Terminal  `yaml:"terminal"`

	// Path is the source file path, set by Load and used in validator
	// messages.
	Path string `yaml:"-"`
}

// StepByID returns the step with the given id, or nil if none exists.
func (w *Workflow) StepByID(id string) *Step {
	for i := range w.Steps {
		if w.Steps[i].ID == id {
			return &w.Steps[i]
		}
	}
	return nil
}

// IsValidTarget reports whether id names a step, a declared terminal, or one
// of the always-implicit terminals done/blocked (Ruling R2).
func (w *Workflow) IsValidTarget(id string) bool {
	if id == "done" || id == "blocked" {
		return true
	}
	if _, ok := w.Terminal[id]; ok {
		return true
	}
	return w.StepByID(id) != nil
}
