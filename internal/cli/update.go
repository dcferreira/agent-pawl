package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/selfupdate"
)

// updateFlags holds pawl update's own flags. VersionGiven is tracked
// separately from Pin so that `--version ""` / `--version=` (an
// explicitly empty value) can be told apart from "no --version at all" —
// both leave Pin == "", but only the former is a usage error.
type updateFlags struct {
	Check        bool
	Pin          string
	VersionGiven bool
	Force        bool
}

// validatePin rejects a --version value that doesn't parse cleanly as
// selfupdate.ParseVersion's MAJOR.MINOR.PATCH (leading "v" tolerated).
// selfupdate.Apply splices the normalized pin straight into a download URL
// path segment (.../releases/download/<tag>/...), so an unvalidated pin is
// a path-traversal-shaped injection point (e.g. "../../x" or
// "v1.2.3/../.."), not just a typo — CmdUpdate calls this before any
// network access, and before resolving anything else about the request.
func validatePin(pin string) error {
	if _, err := selfupdate.ParseVersion(pin); err != nil {
		return fmt.Errorf("pawl update: %q is not a valid version; expected the form vX.Y.Z (e.g. v0.2.0)", pin)
	}
	return nil
}

func parseUpdateArgs(args []string) (updateFlags, error) {
	var f updateFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--check":
			f.Check = true
		case a == "--force":
			f.Force = true
		case a == "--version":
			i++
			if i >= len(args) {
				return f, fmt.Errorf("pawl update: --version needs a value")
			}
			f.Pin = args[i]
			f.VersionGiven = true
		case strings.HasPrefix(a, "--version="):
			f.Pin = strings.TrimPrefix(a, "--version=")
			f.VersionGiven = true
		default:
			return f, fmt.Errorf("pawl update: unrecognised argument %q", a)
		}
	}
	return f, nil
}

// osExecutable and evalSymlinks are os.Executable/filepath.EvalSymlinks,
// indirected through package-level vars so a test can force a resolution
// failure without needing a real broken /proc/self/exe — used by
// TestCmdUpdate_DevRefusalAndCheckDoNotResolveExePath to prove that
// resolveExePath below is never reached by the dev-build refusal or
// --check paths, neither of which needs the running binary's own path.
var (
	osExecutable = os.Executable
	evalSymlinks = filepath.EvalSymlinks
)

// resolveExePath returns cfg.ExePath if already set (the test seam:
// tests always set it), otherwise resolves the running binary's own path
// via osExecutable + evalSymlinks (production's path, when cmd/pawl/
// main.go passes cfg == nil). Deliberately called only from the two
// CmdUpdate branches that actually need to write to that path (the
// no-flags/`--force`/`--version` update path) — not from the dev-build
// refusal or `--check`, neither of which touches the filesystem, so a
// resolution failure must not turn either of those into an unrelated
// exit-5 error.
func resolveExePath(cfg selfupdate.Config) (string, error) {
	if cfg.ExePath != "" {
		return cfg.ExePath, nil
	}
	exe, err := osExecutable()
	if err != nil {
		return "", fmt.Errorf("locating the running binary: %w", err)
	}
	real, err := evalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolving the running binary's path: %w", err)
	}
	return real, nil
}

// CmdUpdate implements `pawl update [--check] [--version <vX.Y.Z>]
// [--force]` (docs/cli.md). currentVersion is the build-time version
// string (main.Version — "dev" for a plain `go build`/`go install`); cfg,
// when non-nil, overrides the production selfupdate.Config (this is the
// seam tests use to point APIBase/DownloadBase at an httptest.Server and
// ExePath at a file inside t.TempDir(), instead of ever calling
// os.Executable or hitting the real network). cmd/pawl/main.go calls this
// the same way it handles "version" itself, since only main.go knows the
// build-time Version string and internal/cli must not import main.
//
// Exit codes: 0 success/no-op/--check; 2 usage error (bad flag, missing
// flag value, empty --version, --check combined with --version, or a
// --version value that isn't a clean vX.Y.Z); 4 refused — the dev-build
// build refusal below, the same "declining to act because the current
// state doesn't match what was asked" bucket docs/cli.md's shared
// exit-code table uses for pawl submit/abandon's refusals, even though
// its listed examples are all run/submit-specific; 5 anything else that
// goes wrong resolving, downloading, verifying or installing the release
// (network failure, checksum mismatch, missing archive entry, unwritable
// target directory, or the "impossible" case of a CompareVersions error
// against an already-validated target version — see below).
func CmdUpdate(args []string, stdout, stderr io.Writer, currentVersion string, cfg *selfupdate.Config) int {
	flags, err := parseUpdateArgs(args)
	if err != nil {
		printLine(stderr, err.Error())
		return 2
	}
	if flags.VersionGiven && flags.Pin == "" {
		printLine(stderr, "pawl update: --version needs a non-empty value")
		return 2
	}
	if flags.Check && flags.VersionGiven {
		printLine(stderr, "pawl update: --check and --version are mutually exclusive")
		return 2
	}
	if flags.Pin != "" {
		if err := validatePin(flags.Pin); err != nil {
			printLine(stderr, err.Error())
			return 2
		}
	}

	resolved := selfupdate.Config{}
	if cfg != nil {
		resolved = *cfg
	}
	resolved = resolved.WithDefaults()

	_, parseErr := selfupdate.ParseVersion(currentVersion)
	isDevBuild := parseErr != nil

	if isDevBuild && !flags.Check && !flags.Force {
		w := &blockWriter{}
		w.line(0, fmt.Sprintf("pawl update: current version %q looks like a source/`go install` build, not a tagged release; pawl update won't overwrite it.", currentVersion))
		w.line(0, "To update a source build, either:")
		w.line(1, "go install github.com/dcferreira/agent-pawl/cmd/pawl@latest")
		w.line(1, "or rebuild from source (see docs/install.md)")
		w.line(0, "--force replaces it with a release binary anyway (see docs/cli.md).")
		fmt.Fprint(stderr, w.String())
		return 4
	}

	if flags.Check {
		tag, err := selfupdate.ResolveTag(resolved, "")
		if err != nil {
			printLine(stderr, "pawl update:", err.Error())
			return 5
		}
		latest := selfupdate.VersionNoV(tag)
		if isDevBuild {
			printLine(stdout, fmt.Sprintf("current: %s (source/go-install build) latest: %s", currentVersion, latest))
		} else {
			cmp, cerr := selfupdate.CompareVersions(currentVersion, latest)
			available := cerr == nil && cmp < 0
			printLine(stdout, fmt.Sprintf("current: %s latest: %s update available: %v", currentVersion, latest, available))
		}
		return 0
	}

	exePath, err := resolveExePath(resolved)
	if err != nil {
		printLine(stderr, "pawl update:", err.Error())
		return 5
	}
	resolved.ExePath = exePath

	var tag string
	if flags.Pin != "" {
		tag = selfupdate.NormalizeTag(flags.Pin)
	} else {
		tag, err = selfupdate.ResolveTag(resolved, "")
		if err != nil {
			printLine(stderr, "pawl update:", err.Error())
			return 5
		}
	}
	targetVersion := selfupdate.VersionNoV(tag)

	if !flags.Force && !isDevBuild {
		// Both currentVersion (isDevBuild is false, so it parsed) and
		// targetVersion (ResolveTag/validatePin already required it to
		// parse cleanly) are known-good vX.Y.Z strings at this point, so
		// CompareVersions returning an error here should be impossible.
		// Fail loudly instead of silently falling through to Apply if it
		// somehow happens anyway — better a confusing-but-safe exit 5
		// than an update whose "already up to date"/"don't downgrade"
		// guard silently never ran.
		cmp, cerr := selfupdate.CompareVersions(currentVersion, targetVersion)
		if cerr != nil {
			printLine(stderr, fmt.Sprintf("pawl update: internal error: comparing %q to %q: %s", currentVersion, targetVersion, cerr.Error()))
			return 5
		}
		switch {
		case cmp == 0:
			if flags.Pin != "" {
				printLine(stdout, fmt.Sprintf("already on %s", currentVersion))
			} else {
				printLine(stdout, fmt.Sprintf("already up to date (%s)", currentVersion))
			}
			return 0
		case cmp > 0 && flags.Pin == "":
			printLine(stdout, fmt.Sprintf("current version %s is newer than latest release %s; not downgrading", currentVersion, targetVersion))
			return 0
		}
	}

	if err := selfupdate.Apply(resolved, tag); err != nil {
		printLine(stderr, "pawl update:", err.Error())
		return 5
	}

	printLine(stdout, fmt.Sprintf("pawl updated: %s -> %s (%s)", currentVersion, targetVersion, resolved.ExePath))
	return 0
}
