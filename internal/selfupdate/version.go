package selfupdate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// versionPattern is deliberately strict semver-subset validation, compiled
// once: a bare MAJOR.MINOR.PATCH (no leading zeros — [1-9][0-9]* forbids
// them, unlike strconv.Atoi alone, which happily accepts "01") with an
// optional "-<prerelease>" suffix restricted to [0-9A-Za-z.-]. This
// matters beyond parsing correctness: internal/cli's validatePin uses
// ParseVersion as the sole gate before a --version value is spliced into
// a GitHub release download URL path segment, so anything this pattern
// lets through that isn't a plain version number is a potential
// path-traversal/injection primitive (e.g. a "-" prerelease suffix
// containing "/" or ".."). The archive AND checksums.txt are both fetched
// from the same attacker-controlled tag, so a traversal here would defeat
// the checksum verification too, not just the download.
var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

// Version is a parsed MAJOR.MINOR.PATCH, with an optional pre-release
// suffix (stored without the leading "-"), ordered by CompareVersions
// per semver 2.0.0 §11.
type Version struct {
	Major, Minor, Patch int
	PreRelease          string
}

// ParseVersion parses a MAJOR.MINOR.PATCH version string, tolerating a
// leading "v" (as GitHub release tags have) and an optional
// "-<prerelease>" suffix. Anything else — including "dev" (main.go's
// unstamped default) and any other unparseable string — is an error: the
// caller of this package (internal/cli's `pawl update`) treats a
// ParseVersion failure on the *current* version as "this is a source/`go
// install` build," which is exactly the case an unstamped `dev` binary
// hits.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("selfupdate: %q is not a MAJOR.MINOR.PATCH version", s)
	}
	// The pattern's own [1-9][0-9]*|0 alternation already rules out
	// leading zeros and non-digit characters, so these three Atoi calls
	// cannot fail — any failure here would be this function disagreeing
	// with its own regexp, which would be a bug worth a panic-shaped
	// error, not a silent accept.
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	patch, err3 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return Version{}, fmt.Errorf("selfupdate: %q is not a MAJOR.MINOR.PATCH version", s)
	}
	pre := strings.TrimPrefix(m[4], "-")
	return Version{Major: major, Minor: minor, Patch: patch, PreRelease: pre}, nil
}

// CompareVersions compares a to b, returning -1/0/1 like strings.Compare.
// Either being unparseable (see ParseVersion — "dev" included) is an
// error: an unparseable version compares to nothing, it's "unknown," and
// the caller must handle that case itself rather than get a false
// ordering out of this function.
func CompareVersions(a, b string) (int, error) {
	va, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	if c := cmp(va.Major, vb.Major); c != 0 {
		return c, nil
	}
	if c := cmp(va.Minor, vb.Minor); c != 0 {
		return c, nil
	}
	if c := cmp(va.Patch, vb.Patch); c != 0 {
		return c, nil
	}
	// Same MAJOR.MINOR.PATCH: a pre-release is older than the plain
	// release it precedes; two pre-releases follow semver 2.0.0 §11.
	switch {
	case va.PreRelease == vb.PreRelease:
		return 0, nil
	case va.PreRelease == "":
		return 1, nil
	case vb.PreRelease == "":
		return -1, nil
	default:
		return comparePreRelease(va.PreRelease, vb.PreRelease), nil
	}
}

// comparePreRelease orders two non-empty pre-release strings (without the
// leading "-") by semver 2.0.0 §11: dot-separated identifiers compare left
// to right; all-digit identifiers compare numerically and rank below
// alphanumeric ones, which compare in ASCII order; if every shared
// identifier is equal, the longer list wins.
func comparePreRelease(a, b string) int {
	ia, ib := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ia) && i < len(ib); i++ {
		if c := compareIdent(ia[i], ib[i]); c != 0 {
			return c
		}
	}
	return cmp(len(ia), len(ib))
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareIdent(a, b string) int {
	na, nb := isNumeric(a), isNumeric(b)
	switch {
	case na && nb:
		// Numeric compare without overflow: strip leading zeros, then
		// longer is larger, equal length compares lexically.
		a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
		if c := cmp(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case na:
		return -1
	case nb:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func cmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// NormalizeTag ensures a version string has the leading "v" GitHub release
// tags use, e.g. for turning a user-supplied `--version 0.2.0` into the
// tag `v0.2.0`.
func NormalizeTag(s string) string {
	if strings.HasPrefix(s, "v") {
		return s
	}
	return "v" + s
}

// VersionNoV strips a leading "v", matching goreleaser's {{.Version}}
// template value used in archive names (mirrors install.sh's
// pawl_version_no_v).
func VersionNoV(s string) string {
	return strings.TrimPrefix(s, "v")
}
