package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// attemptKeyHashLen is how many hex characters of the sha256 digest are kept
// (design/format-spec.md §B.4: "a hash ... truncated prefix").
const attemptKeyHashLen = 16

// normalizeWhitespace collapses every run of whitespace to a single space
// and trims the ends, so two failure texts that differ only in incidental
// spacing (a trailing newline, a script's indentation) key the same attempt
// budget (design/format-spec.md §B.4).
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// hashFailureText is the automatic attempt_key: a sha256 of the
// whitespace-normalised postcondition failure text, truncated. It lives here
// (internal/engine), not in internal/journal, by ruling: journal only stores
// and keys on the string it is given.
func hashFailureText(text string) string {
	norm := normalizeWhitespace(text)
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])[:attemptKeyHashLen]
}
