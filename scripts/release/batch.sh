#!/usr/bin/env bash
# Runs `changie batch` for the Release PR workflow, including the release
# candidate flow. Run from the repo root (the workflow's cwd).
#
# Usage: batch.sh <version-input> [<prerelease>]
#   version-input  empty/auto, major, minor, patch, vX.Y.Z or X.Y.Z
#   prerelease     optional rc.N (N >= 1), to cut v<version>-rc.N
#
# Why this is a script: the rc strategy has rules that must be refused
# BEFORE changie runs, and refusing them is unit-tested in test-checks.sh
# (no changie needed for those cases; the end-to-end proof uses real changie).
#
# The three modes, chosen by the arguments and by whether .changes/rc/ holds
# parked fragments:
#   rc cut      (prerelease given)  `changie batch vX.Y.Z --prerelease rc.N
#               --move-dir rc`: writes .changes/vX.Y.Z-rc.N.md with only the
#               unreleased delta and parks those fragments in .changes/rc/.
#               The unreleased fragments are required.
#   final cut   (no prerelease, .changes/rc/ pending) `changie batch vX.Y.Z
#               --include rc --remove-prereleases`: one vX.Y.Z.md holding the
#               parked and any new fragments, and the rc dir and rc notes
#               removed. Allowed with nothing new in unreleased/.
#   normal cut  (neither)  `changie batch <version>`; unreleased/ is required.
#
# The version must be explicit vX.Y.Z for an rc cut and for a final cut with
# an rc pending: "auto"/major/minor/patch would be computed by changie from
# the fragments (or from the latest rc), not from the version the rc line is
# targeting, so it could silently pick something other than what was tested.
#
# `changie merge` and reading the new version back stay in the workflow.
#
# Sourced by test-checks.sh; guarded at the bottom by PAWL_RELEASE_CHECK_TEST
# like the other release scripts.
set -euo pipefail

# release_batch VERSION_INPUT [PRERELEASE]
release_batch() {
  local input="${1:-}" pre="${2:-}" version
  version="${input:-auto}"
  # changie preserves the version string as given: an explicit "1.2.3" (no
  # leading v) would write .changes/1.2.3.md, which check-version-consistency.sh
  # (only globs .changes/v*.md) and the release/v* branch-name exemption both
  # miss. Normalize a leading-digit input to the v-prefixed form;
  # "auto"/"major"/"minor"/"patch" and an already-prefixed "v1.2.3" pass through.
  case "$version" in
  [0-9]*) version="v$version" ;;
  esac

  local explicit=0
  if [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    explicit=1
  fi

  if [ -n "$pre" ] && ! [[ "$pre" =~ ^rc\.[1-9][0-9]*$ ]]; then
    echo "::error::prerelease must look like rc.N with N >= 1 (e.g. rc.1), got '$pre'." >&2
    return 1
  fi

  local have_unreleased=0 have_rc=0
  compgen -G ".changes/unreleased/*.yaml" >/dev/null && have_unreleased=1
  compgen -G ".changes/rc/*.yaml" >/dev/null && have_rc=1

  if [ -n "$pre" ]; then
    if [ "$explicit" -ne 1 ]; then
      echo "::error::a release candidate needs an explicit version (vX.Y.Z), got '$version': changie can't compute the target version of an rc line from the fragments." >&2
      return 1
    fi
    if [ "$have_unreleased" -ne 1 ]; then
      echo "::error::.changes/unreleased/ has no fragments — nothing to put in $version-$pre. Every PR since the last release should have added one (see docs/releasing.md)." >&2
      return 1
    fi
    changie batch "$version" --prerelease "$pre" --move-dir rc
    return
  fi

  if [ "$have_rc" -eq 1 ]; then
    if [ "$explicit" -ne 1 ]; then
      echo "::error::.changes/rc/ holds release-candidate fragments, so the final release needs an explicit version (vX.Y.Z), got '$version': auto/major/minor/patch would be computed from the rc, not the version it targets." >&2
      return 1
    fi
    changie batch "$version" --include rc --remove-prereleases
    return
  fi

  if [ "$have_unreleased" -ne 1 ]; then
    echo "::error::.changes/unreleased/ has no fragments — nothing to release. Every PR since the last release should have added one (see docs/releasing.md)." >&2
    return 1
  fi
  changie batch "$version"
}

main() {
  release_batch "$@"
}

if [ "${PAWL_RELEASE_CHECK_TEST:-0}" != "1" ]; then
  main "$@"
fi
