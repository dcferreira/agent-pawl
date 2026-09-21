package spec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultMaxSteps is the workflow-level backstop on total step entries in a
// run (design/format-spec.md §D).
const DefaultMaxSteps = 200

// DefaultMaxVisits is the cap on entries to a single step in one run
// (design/format-spec.md §D).
const DefaultMaxVisits = 10

// DefaultAttempts is the number of times a step runs before its
// postcondition failure is treated as final (design/format-spec.md §D).
const DefaultAttempts = 1

// DefaultEmits is the payload grammar assumed when emits: is omitted
// (design/format-spec.md §D).
const DefaultEmits = "json"

// DefaultEvery is the poll interval assumed when every: is omitted on a
// wait step (design/format-spec.md §D).
const DefaultEvery = "60s"

// Load reads path, parses it as a workflow file and applies the
// design/format-spec.md §D defaults, so that no later package has to
// interpret a zero value.
//
// The decoder runs with KnownFields(true) (Finding F6): a field name this
// build does not recognise at the workflow, args:, state:, guards:,
// invariants:, terminal: or catch: level is a load error naming the file,
// rather than a silently-dropped typo. A step-level unknown field is instead
// recorded on Step.UnknownFields and reported by Validate, since Step has
// its own UnmarshalYAML (see step.go) and so bypasses the decoder's
// field-name checking for its own subtree.
func Load(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("spec: reading %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var w Workflow
	if err := dec.Decode(&w); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("spec: parsing %s: %w", path, err)
	}
	w.Path = path
	applyDefaults(&w)
	return &w, nil
}

// applyDefaults fills in the design/format-spec.md §D defaults. It is
// idempotent and safe to call more than once (Validate also calls it
// defensively, per Finding F8, for a *Workflow that did not come through
// Load — a test literal, or a future journal round-trip).
//
// A resolved value is clamped to the default whenever its *Raw witness is
// present but invalid (e.g. attempts: 0): the *Raw field stays the invalid
// value so validator rule 12/13 can still report it, but the resolved field
// a later package reads is never zero or negative (Finding F8).
func applyDefaults(w *Workflow) {
	if w.MaxStepsRaw != nil && *w.MaxStepsRaw >= 1 {
		w.MaxSteps = *w.MaxStepsRaw
	} else {
		w.MaxSteps = DefaultMaxSteps
	}

	for i := range w.Steps {
		s := &w.Steps[i]

		if s.AttemptsRaw != nil && *s.AttemptsRaw >= 1 {
			s.Attempts = *s.AttemptsRaw
		} else {
			s.Attempts = DefaultAttempts
		}

		if s.MaxVisitsRaw != nil && *s.MaxVisitsRaw >= 1 {
			s.MaxVisits = *s.MaxVisitsRaw
		} else {
			s.MaxVisits = DefaultMaxVisits
		}

		if s.Emits == "" {
			s.Emits = DefaultEmits
		}

		if s.Kind == "wait" && s.Every == "" {
			s.Every = DefaultEvery
		}
	}
}
