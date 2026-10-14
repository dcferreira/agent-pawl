#!/usr/bin/env bash
# Succeeds (exit 0) when a release version is a semver prerelease, e.g.
# v1.0.0-rc.1; fails (exit 1) for a plain X.Y.Z.
#
# Why this exists: release.yml must not point the repo's own
# .claude-plugin/marketplace.json at a release candidate — that file is what
# `/plugin marketplace add dcferreira/agent-pawl` installs from, so a
# prerelease there would hand every marketplace user the rc. The workflow
# runs this on the resolved version and sets SKIP_MARKETPLACE=1 when it
# succeeds. It is a separate script (rather than inline shell) so the rule
# is unit-tested in test-checks.sh.
#
# Usage: is-prerelease.sh <version>   (leading "v" optional)
# Prints nothing; the exit status is the answer (2 on a usage error).
#
# Sourced by test-checks.sh; guarded at the bottom by PAWL_RELEASE_CHECK_TEST
# like the other release scripts.
set -euo pipefail

# is_prerelease VERSION
# A semver prerelease is introduced by the first "-"; X.Y.Z never has one.
is_prerelease() {
  case "$1" in
  *-*) return 0 ;;
  *) return 1 ;;
  esac
}

main() {
  if [ "$#" -ne 1 ] || [ -z "$1" ]; then
    echo "usage: is-prerelease.sh <version>" >&2
    return 2
  fi
  is_prerelease "$1"
}

if [ "${PAWL_RELEASE_CHECK_TEST:-0}" != "1" ]; then
  main "$@"
fi
