package engine

import "errors"

// Tests-first stub for `pawl run --from` (#94): replaced by from.go in the
// next commit.

// ErrFromTarget is stubbed so the tests compile.
var ErrFromTarget = errors.New("engine: --from target refused")

// ResumeFrom is not implemented yet.
func (e *Engine) ResumeFrom(runID, target string, force bool) (Instruction, error) {
	return nil, errors.New("engine: ResumeFrom: not implemented")
}
