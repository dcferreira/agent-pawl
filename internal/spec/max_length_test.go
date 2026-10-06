package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxLenWorkflow wraps state:/writes: snippets in a minimal agentic workflow.
func maxLenWorkflow(state, writes string) string {
	return "workflow: ml\nstart: a\nstate:\n" + state + "steps:\n  - id: a\n    kind: agentic\n    description: d\n    writes: " + writes + "\n    postcondition: \"true\"\n    next: done\nterminal: {done: {status: ok}}\n"
}

func loadInline(t *testing.T, doc string) (*Workflow, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wf.yaml")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func validateInline(t *testing.T, doc string) []string {
	t.Helper()
	w, err := loadInline(t, doc)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r, err := Validate(w)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return r.Errors
}

func TestWrites_MaxLengthParsed(t *testing.T) {
	w := decodeWrites(t, "title: {type: string, max_length: 72}\nn: {type: integer}\n")
	if w.MaxLength["title"] != 72 {
		t.Fatalf("MaxLength = %v, want title=72", w.MaxLength)
	}
	if _, ok := w.MaxLength["n"]; ok {
		t.Fatalf("MaxLength = %v, n must have no entry", w.MaxLength)
	}
}

func TestLoad_WritesUnknownFieldRejected(t *testing.T) {
	_, err := loadInline(t, maxLenWorkflow("  title: {type: string}\n", "{title: {type: string, max_lenght: 72}}"))
	if err == nil {
		t.Fatal("Load: expected an error for an unknown writes entry field")
	}
	for _, want := range []string{`step "a"`, "title", "max_lenght"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Load error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestWrites_MergeKeyStillWorksWithMaxLength(t *testing.T) {
	w := decodeWrites(t, "base: &b {type: string, max_length: 5}\nother:\n  <<: *b\n")
	if w.Types["other"] != "string" || w.MaxLength["other"] != 5 {
		t.Fatalf("merged entry = type %q max_length %d, want string/5", w.Types["other"], w.MaxLength["other"])
	}
}

func TestValidate_MaxLength(t *testing.T) {
	cases := []struct {
		name, state, writes string
		want                string // substring of the single expected error; "" = none
	}{
		{"valid state cap", "  title: {type: string, max_length: 72}\n", "{title: {type: string}}", ""},
		{"valid writes cap", "  title: {type: string}\n", "{title: {type: string, max_length: 72}}", ""},
		{"default at limit", "  title: {type: string, default: abc, max_length: 3}\n", "{title: {type: string}}", ""},
		{"negative state", "  title: {type: string, max_length: -1}\n", "{title: {type: string}}", "rule 29: state: title: max_length: -1 is negative"},
		{"negative writes", "  title: {type: string}\n", "{title: {type: string, max_length: -2}}", "rule 29: writes: title: max_length: -2 is negative"},
		{"non-string state", "  n: {type: integer, max_length: 3}\n", "{n: {type: integer}}", "rule 29: state: n: max_length: is only valid on type: string, not type: integer"},
		{"non-string writes", "  n: {type: integer}\n", "{n: {type: integer, max_length: 3}}", "rule 29: writes: n: max_length: is only valid on type: string, not type: integer"},
		{"default too long", "  title: {type: string, default: abcd, max_length: 3}\n", "{title: {type: string}}", "rule 29: state: title: default: is 4 characters, longer than max_length 3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateInline(t, maxLenWorkflow(tc.state, tc.writes))
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("errors = %v, want none", errs)
				}
				return
			}
			if len(errs) != 1 || !strings.Contains(errs[0], tc.want) {
				t.Fatalf("errors = %v, want one containing %q", errs, tc.want)
			}
		})
	}
}

func TestMaxLengthFor_TighterWins(t *testing.T) {
	w, err := loadInline(t, maxLenWorkflow("  title: {type: string, max_length: 10}\n", "{title: {type: string, max_length: 5}}"))
	if err != nil {
		t.Fatal(err)
	}
	s := w.StepByID("a")
	if got := MaxLengthFor(w.State, s, "title"); got != 5 {
		t.Fatalf("got %d, want 5 (tighter writes cap)", got)
	}
	s.Writes.MaxLength["title"] = 20
	if got := MaxLengthFor(w.State, s, "title"); got != 10 {
		t.Fatalf("got %d, want 10 (tighter state cap)", got)
	}
	s.Writes.MaxLength = nil
	w.State["title"] = StateDecl{Type: "string"}
	if got := MaxLengthFor(w.State, s, "title"); got != 0 {
		t.Fatalf("got %d, want 0 (no cap)", got)
	}
}
