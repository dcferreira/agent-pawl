package journal

import (
	"errors"
	"fmt"
)

// Format is the on-disk journal format this build WRITES: the value stamped
// on RUN_START (Event.Format) and on plan.json ("format"). This build reads
// every format in 1..Format; a missing or 0 stamp means format 1 (see
// NormalizeFormat), the format that predates stamping.
//
// The rules:
//
//   - Adding an event kind, or changing plan.json's shape, bumps Format.
//   - A later binary reads every earlier format (decode paths for old
//     formats are kept, and covered by the fixtures under testdata/compat).
//   - Downgrade is not promised: an older binary meeting a newer stamp
//     refuses with a *FormatError (errors.Is ErrNewerFormat) rather than
//     guess at records it does not understand.
//
// History: 1 = unstamped; plan.json serialised spec.Workflow with Go field
// names. 2 = stamped; plan.json uses explicit snake_case json tags.
const Format = 2

// ErrNewerFormat is the sentinel a *FormatError matches under errors.Is.
var ErrNewerFormat = errors.New("journal: written by a newer pawl")

// FormatError reports a journal or plan stamped with a format this build
// does not read.
type FormatError struct {
	Format int
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("journal: written by a newer pawl (format %d; this pawl reads formats 1-%d)", e.Format, Format)
}

// Is makes errors.Is(err, ErrNewerFormat) true for every FormatError.
func (e *FormatError) Is(target error) bool { return target == ErrNewerFormat }

// NormalizeFormat maps the missing stamp (0) to format 1.
func NormalizeFormat(f int) int {
	if f == 0 {
		return 1
	}
	return f
}

// checkFormat returns a *FormatError when stamp is newer than Format.
func checkFormat(stamp int) error {
	if n := NormalizeFormat(stamp); n > Format {
		return &FormatError{Format: n}
	}
	return nil
}
