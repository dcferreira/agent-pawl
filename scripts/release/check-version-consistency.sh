#!/usr/bin/env bash
# Fails unless .claude-plugin/plugin.json's "version" matches the latest
# released version recorded under .changes/, and (when present) the repo's
# .claude-plugin/marketplace.json points at a real release. Runs in CI on every PR and on
# main (see ci.yml's lint job) as a sanity check that the two never drift,
# and again in release.yml right after a Release PR merges.
#
# Usage: check-version-consistency.sh [repo-dir]
# repo-dir defaults to ".".
#
# The marketplace check: the agent-pawl entry of <repo-dir>/.claude-plugin/
# marketplace.json must be an `archive` source whose url is the release-asset
# url for some version V with a .changes/vV.md (a released version), tag and
# zip name agreeing, and a 64-char lowercase hex sha256. V is deliberately NOT
# required to equal plugin.json's version: release.yml's follow-up commit
# (update-repo-marketplace.sh) lands AFTER the release PR merges, so in the
# release PR itself, and between its merge and that commit, plugin.json is one
# release ahead of the marketplace entry. Skipped when the file is absent
# (the test fixtures).
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

# check_marketplace DIR
# See the header comment: validates the repo marketplace's agent-pawl entry.
check_marketplace() {
  local dir="$1" mp="$1/.claude-plugin/marketplace.json"
  [ -f "$mp" ] || return 0
  local src url sha ver
  src="$(jq -c '[.plugins[]? | select(.name == "agent-pawl")][0].source // empty' "$mp")" || {
    echo "FAIL: $mp is not valid JSON." >&2
    return 1
  }
  if [ -z "$src" ]; then
    echo "FAIL: $mp has no plugin named agent-pawl (with a source)." >&2
    return 1
  fi
  if [ "$(jq -r 'if type == "object" then (.source // "") else "" end' <<<"$src")" != "archive" ]; then
    echo "FAIL: $mp's agent-pawl source must be an archive source pointing at a release zip (got $src). Run scripts/release/update-repo-marketplace.sh." >&2
    return 1
  fi
  url="$(jq -r '.url // ""' <<<"$src")"
  sha="$(jq -r '.sha256 // ""' <<<"$src")"
  local re='^https://github\.com/dcferreira/agent-pawl/releases/download/v([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)/agent-pawl-plugin_([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)\.zip$'
  if ! [[ "$url" =~ $re ]] || [ "${BASH_REMATCH[1]}" != "${BASH_REMATCH[3]}" ]; then
    echo "FAIL: $mp's agent-pawl url ($url) isn't https://github.com/dcferreira/agent-pawl/releases/download/vX.Y.Z/agent-pawl-plugin_X.Y.Z.zip." >&2
    return 1
  fi
  ver="${BASH_REMATCH[1]}"
  if [ ! -f "$dir/.changes/v$ver.md" ]; then
    echo "FAIL: $mp points at v$ver, but there is no .changes/v$ver.md (not a released version)." >&2
    return 1
  fi
  if ! [[ "$sha" =~ ^[0-9a-f]{64}$ ]]; then
    echo "FAIL: $mp's agent-pawl sha256 ($sha) isn't 64 lowercase hex characters." >&2
    return 1
  fi
  echo "ok: marketplace.json points at released v$ver"
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
  check_marketplace "$dir"
  return $?
}

main() {
  check_version_consistency "${1:-.}"
}

if [ "${PAWL_RELEASE_CHECK_TEST:-0}" != "1" ]; then
  main "$@"
fi
