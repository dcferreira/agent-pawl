package engine

import "testing"

func TestHashFailureText_WhitespaceNormalised(t *testing.T) {
	a := hashFailureText("line one\nline two   with   spaces\n")
	b := hashFailureText("line one line two with spaces")
	if a != b {
		t.Errorf("hashFailureText should whitespace-normalise before hashing: %q != %q", a, b)
	}
}

func TestHashFailureText_DifferentTextDifferentKey(t *testing.T) {
	a := hashFailureText("failure one")
	b := hashFailureText("failure two")
	if a == b {
		t.Errorf("hashFailureText(%q) == hashFailureText(%q); want distinct keys", "failure one", "failure two")
	}
}

func TestHashFailureText_Deterministic(t *testing.T) {
	a := hashFailureText("some failure text")
	b := hashFailureText("some failure text")
	if a != b {
		t.Errorf("hashFailureText is not deterministic: %q != %q", a, b)
	}
}
