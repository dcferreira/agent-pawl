#!/usr/bin/env bash
# Fails unless .claude-plugin/plugin.json's "version" matches the latest
# released version recorded under .changes/. Runs in CI on every PR and on
# main (see ci.yml's lint job) as a sanity check that the two never drift,
# and again in release.yml right after a Release PR merges.
#
# Usage: check-version-consistency.sh [repo-dir]
# repo-dir defaults to ".".
#
# Deliberately doesn't shell out to changie: "latest" is just the highest
# semver among .changes/v*.md filenames, computed here with `sort -V` so
# this check has no tooling dependency beyond git/jq/coreutils (CI installs
# changie anyway, for release-pr.yml and release.yml, but this script
# doesn't need it). Prerelease filenames (a "-" after the version, e.g.
# v1.0.0-rc1.md) are excluded before sorting: `sort -V` orders a
# prerelease AFTER its final release — the opposite of changie/semver
# precedence — so if one were left in, it would win the "latest" pick
# whenever a prerelease file sits next to its release. This repo only
# ever writes plain X.Y.Z release-notes filenames, so excluding anything
# with a "-" is enough, and `sort -V` is correct for ordering those.
#
# Sourced by test-checks.sh; guarded at the bottom by
# PAWL_RELEASE_CHECK_TEST like the other two release check scripts.
set -euo pipefail

# latest_version_from_changes DIR
# Echoes the highest vX.Y.Z among DIR/.changes/v*.md filenames, or nothing
# (and a non-zero exit) if there are none. Filenames with a "-" after the
# version (prereleases, e.g. v1.0.0-rc1.md) are excluded — see the header
# comment above for why.
latest_version_from_changes() {
  local dir="$1"
  local f base latest=""
  shopt -s nullglob
  for f in "$dir"/.changes/v*.md; do
    base="$(basename "$f" .md)"
    case "$base" in
    *-*) continue ;;
    esac
    latest="${latest:+$latest$'\n'}$base"
  done
  shopt -u nullglob
  [ -n "$latest" ] || return 1
  echo "$latest" | sort -V | tail -n1
}

# plugin_version DIR
plugin_version() {
  local dir="$1"
  jq -r '.version // empty' "$dir/.claude-plugin/plugin.json" 2>/dev/null
}

check_version_consistency() {
  local dir="${1:-.}"
  local latest plugin_ver latest_no_v

  if ! latest=$(latest_version_from_changes "$dir"); then
    echo "FAIL: no .changes/v*.md release-notes files found under $dir/.changes — can't determine the latest version." >&2
    return 1
  fi
  latest_no_v="${latest#v}"

  plugin_ver=$(plugin_version "$dir")
  if [ -z "$plugin_ver" ]; then
    echo "FAIL: couldn't read .version from $dir/.claude-plugin/plugin.json." >&2
    return 1
  fi

  if [ "$plugin_ver" != "$latest_no_v" ]; then
    echo "FAIL: .claude-plugin/plugin.json version ($plugin_ver) != latest released version ($latest_no_v, from .changes/$latest.md). Run the Release PR workflow, which bumps plugin.json via changie's replacements:." >&2
    return 1
  fi

  echo "ok: plugin.json version ($plugin_ver) matches latest release ($latest)"
  return 0
}

main() {
  check_version_consistency "${1:-.}"
}

if [ "${PAWL_RELEASE_CHECK_TEST:-0}" != "1" ]; then
  main "$@"
fi
