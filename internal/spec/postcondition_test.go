package spec

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func decodePostcondition(t *testing.T, doc string) Postcondition {
	t.Helper()
	var p Postcondition
	if err := yaml.Unmarshal([]byte(doc), &p); err != nil {
		t.Fatalf("unmarshal %q: %v", doc, err)
	}
	return p
}

func TestPostcondition_BareString(t *testing.T) {
	p := decodePostcondition(t, `ruff format --check .`)
	if !p.HasCommand || p.Command != "ruff format --check ." {
		t.Fatalf("got %+v", p)
	}
	if p.altCount() != 1 {
		t.Fatalf("altCount = %d, want 1", p.altCount())
	}
}

func TestPostcondition_MapCommand(t *testing.T) {
	p := decodePostcondition(t, "command: scripts/check.sh\nsoft: true\n")
	if !p.HasCommand || p.Command != "scripts/check.sh" {
		t.Fatalf("got %+v", p)
	}
	if !p.Soft || !p.SoftSet {
		t.Fatalf("expected soft set, got %+v", p)
	}
}

func TestPostcondition_MapAllSet(t *testing.T) {
	p := decodePostcondition(t, "all_set: [branch, title]\n")
	if !p.HasAllSet {
		t.Fatalf("got %+v", p)
	}
	if len(p.AllSet) != 2 || p.AllSet[0] != "branch" || p.AllSet[1] != "title" {
		t.Fatalf("got %+v", p.AllSet)
	}
}

func TestPostcondition_MapEquals(t *testing.T) {
	p := decodePostcondition(t, "equals: {status: green}\n")
	if !p.HasEquals {
		t.Fatalf("got %+v", p)
	}
	if p.Equals["status"] != "green" {
		t.Fatalf("got %+v", p.Equals)
	}
}

func TestPostcondition_TwoKeysAtOnce(t *testing.T) {
	p := decodePostcondition(t, "command: scripts/check.sh\nall_set: [branch]\n")
	if p.altCount() != 2 {
		t.Fatalf("altCount = %d, want 2 (validator rule 14 reports this, not the decoder)", p.altCount())
	}
}

func TestPostcondition_UnknownKey(t *testing.T) {
	p := decodePostcondition(t, "command: scripts/check.sh\nbogus: 1\n")
	if len(p.UnknownKeys) != 1 || p.UnknownKeys[0] != "bogus" {
		t.Fatalf("got %+v", p.UnknownKeys)
	}
}
