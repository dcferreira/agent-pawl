package spec

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func decodeContextEntry(t *testing.T, doc string) ContextEntry {
	t.Helper()
	var c ContextEntry
	if err := yaml.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatalf("unmarshal %q: %v", doc, err)
	}
	return c
}

// TestContextEntry_PlainScalarIsAFile is finding A1's core case: a plain
// context: entry names a file, and must not be mistaken for a command.
func TestContextEntry_PlainScalarIsAFile(t *testing.T) {
	c := decodeContextEntry(t, `notes.md`)
	if c.IsCmd {
		t.Errorf("got IsCmd = true, want false for a plain scalar")
	}
	if c.Value != "notes.md" {
		t.Errorf("got Value = %q, want \"notes.md\"", c.Value)
	}
	if c.Tag != "!!str" {
		t.Errorf("got Tag = %q, want \"!!str\"", c.Tag)
	}
}

// TestContextEntry_CmdTag is finding A1: a "!cmd"-tagged entry must be
// captured as a command, not silently decoded as a plain string with the
// tag information discarded (the gap the original Task 6 submission
// flagged and the reviewer ruled must be fixed properly).
func TestContextEntry_CmdTag(t *testing.T) {
	c := decodeContextEntry(t, `!cmd "jj diff"`)
	if !c.IsCmd {
		t.Errorf("got IsCmd = false, want true for a !cmd-tagged scalar")
	}
	if c.Value != "jj diff" {
		t.Errorf("got Value = %q, want \"jj diff\"", c.Value)
	}
}

// TestContextEntry_ListOfMixedEntries decodes a context: list containing
// both forms, as it appears inside a real step.
func TestContextEntry_ListOfMixedEntries(t *testing.T) {
	var entries []ContextEntry
	doc := "- notes.md\n- !cmd \"jj diff\"\n- !cmd \"git log -1\"\n"
	if err := yaml.Unmarshal([]byte(doc), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	want := []ContextEntry{
		{IsCmd: false, Value: "notes.md", Tag: "!!str"},
		{IsCmd: true, Value: "jj diff", Tag: "!cmd"},
		{IsCmd: true, Value: "git log -1", Tag: "!cmd"},
	}
	for i, w := range want {
		if entries[i] != w {
			t.Errorf("entries[%d] = %+v, want %+v", i, entries[i], w)
		}
	}
}

// TestContextEntry_UnquotedBangTag is reviewer finding A1 on rule 18: an
// UNQUOTED context: entry starting with "!" doesn't stay a plain string at
// all — YAML reads "!git diff main" as a scalar "diff main" carrying the
// custom tag "!git", not as the literal text "!git diff main" with tag
// "!!str" (that only happens when the author quotes it). ContextEntry must
// capture that tag, not silently decode the tag away and keep only the
// (wrong) remaining value, or rule 18 has no way to catch it.
func TestContextEntry_UnquotedBangTag(t *testing.T) {
	c := decodeContextEntry(t, `!git diff main`)
	if c.IsCmd {
		t.Errorf("got IsCmd = true, want false: !git is not !cmd")
	}
	if c.Tag != "!git" {
		t.Errorf("got Tag = %q, want \"!git\"", c.Tag)
	}
	if c.Value != "diff main" {
		t.Errorf("got Value = %q, want \"diff main\" (the tag !git is stripped from the scalar by YAML itself)", c.Value)
	}
}

// TestContextEntry_NonScalarIsRejected: a context: entry must be a string
// (optionally !cmd-tagged), never a list or map.
func TestContextEntry_NonScalarIsRejected(t *testing.T) {
	var c ContextEntry
	if err := yaml.Unmarshal([]byte("[a, b]"), &c); err == nil {
		t.Error("unmarshal of a sequence: want an error, got nil")
	}
}
