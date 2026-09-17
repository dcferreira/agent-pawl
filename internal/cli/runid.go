package cli

import (
	"crypto/rand"
	"encoding/hex"
)

// newRunID generates a short random run id, in the style of DESIGN.md §2's
// worked example ("7f3a"): 2 random bytes as 4 lowercase hex characters. A
// collision is possible but vanishingly unlikely for the number of
// concurrent runs one working copy realistically has, and is not a
// correctness hazard: Engine.Start's own journal operations on that
// directory would simply continue rather than corrupt anything.
func newRunID() string {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}
