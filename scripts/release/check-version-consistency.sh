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
# semver among .changes/v*.md filenames, computed here so this check has no
# tooling dependency beyond git/jq/coreutils (CI installs changie anyway,
# for release-pr.yml and release.yml, but this script doesn't need it).
# Plain `sort -V` gets prerelease ordering backwards (it puts v1.0.0-rc1
# AFTER v1.0.0, and can't compare prereleases of different base versions
# correctly either), so each filename's basename (without ".md") is turned
# into a three-field sort key "<base> <flag> <pre>": base is the X.Y.Z
# part, flag is 0 for a prerelease (has a "-") and 1 for a plain release,
# and pre is the prerelease suffix (empty for a plain release). Sorting
# that key with `sort -k1,1V -k2,2n -k3,3V` orders by base version first,
# then puts a bare release after all of its own prereleases, then orders
# same-base prereleases amongst themselves — matching changie/semver
# precedence while still keeping prereleases as candidates for "latest"
# (needed so a prerelease-only Release PR, e.g. version=v1.0.0-rc1, has
# something for latest_version_from_changes to return).
#
# Sourced by test-checks.sh; guarded at the bottom by
# PAWL_RELEASE_CHECK_TEST like the other two release check scripts.
set -euo pipefail

# latest_version_from_changes DIR
# Echoes the semver-highest vX.Y.Z or vX.Y.Z-PRE among DIR/.changes/v*.md
# filenames, or nothing (and a non-zero exit) if there are none. A bare
# release sorts after its own prereleases; prereleases remain eligible to
# be "latest" when no bare release for that base version exists yet — see
# the header comment above for the sort-key scheme.
latest_version_from_changes() {
  local dir="$1"
  local f base_full version_part base pre flag key
  local keys=""
  shopt -s nullglob
  for f in "$dir"/.changes/v*.md; do
    base_full="$(basename "$f" .md)"
    version_part="${base_full#v}"
    case "$version_part" in
    *-*)
      base="${version_part%%-*}"
      pre="${version_part#*-}"
      flag=0
      ;;
    *)
      base="$version_part"
      pre=""
      flag=1
      ;;
    esac
    key="$base $flag $pre"
    keys="${keys:+$keys$'\n'}$key"
  done
  shopt -u nullglob
  [ -n "$keys" ] || return 1
  local top base_out flag_out pre_out
  top="$(printf '%s\n' "$keys" | sort -k1,1V -k2,2n -k3,3V | tail -n1)"
  read -r base_out flag_out pre_out <<<"$top"
  if [ "$flag_out" = "1" ]; then
    echo "v${base_out}"
  else
    echo "v${base_out}-${pre_out}"
  fi
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
