// Package fixture is the green-tests example's e2e fixture: a tiny module
// with one deliberately broken function, patched in place by scripted
// wf submit calls during the end-to-end test (never by an LLM).
package fixture

// Add returns the sum of a and b.
//
// Deliberately broken: it subtracts instead of adding, so the fixture's
// test suite starts red.
func Add(a, b int) int {
	return a - b
}
