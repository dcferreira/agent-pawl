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
// suffix carried along only to make a same-MAJOR.MINOR.PATCH release sort
// after its own pre-releases (CompareVersions) — nothing here does full
// semver precedence across multiple pre-release identifiers, since this
// project's own tags (.goreleaser.yaml, cmd/pawl/main.go) never use them.
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
	// Same MAJOR.MINOR.PATCH: a pre-release is conservatively treated as
	// older than the release it precedes (and than a differently-named
	// pre-release is treated as equal — this project doesn't tag those,
	// so there's nothing to order them by).
	switch {
	case va.PreRelease == vb.PreRelease:
		return 0, nil
	case va.PreRelease == "":
		return 1, nil
	case vb.PreRelease == "":
		return -1, nil
	default:
		return 0, nil
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
