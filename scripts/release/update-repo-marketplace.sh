#!/usr/bin/env bash
# Points the repo's own .claude-plugin/marketplace.json at a release.
#
# Why this exists: Claude Code treats any github.com URL as a git repo, so a
# release-asset marketplace.json URL can't be added with
# `/plugin marketplace add <url>`. Users instead add the repo itself
# (`/plugin marketplace add dcferreira/agent-pawl`), and that repo marketplace
# lists the plugin with an `archive` source pointing at the latest release's
# plugin zip (which bundles the pawl binaries), pinned by sha256. release.yml
# runs this after uploading the release assets to keep that entry current.
#
# Usage: update-repo-marketplace.sh <release-marketplace.json> [repo-marketplace.json]
#   release-marketplace.json  the marketplace.json build-plugin-archive.sh
#                             wrote (the single source of truth for the zip's
#                             url and sha256)
#   repo-marketplace.json     default .claude-plugin/marketplace.json
#
# Replaces ONLY the `source` of the plugin named agent-pawl; every other
# field and plugin is preserved. The release source is validated first (an
# archive source, a release-asset url whose tag and zip name carry the same
# X.Y.Z[-pre] version, a 64-char lowercase hex sha256): a malformed release
# file fails with a FAIL: message and the repo file is left untouched.
#
# Idempotent and monotonic: an identical source is a no-op (file left
# byte-identical), and an entry already pointing at a NEWER release is never
# downgraded (a re-run of an old release must not undo a newer one) — both
# print a notice and exit 0. A non-archive entry (e.g. "./") is replaced.
# Versions are compared like check-version-consistency.sh orders releases
# (see its header): base X.Y.Z numerically, a bare release after its own
# prereleases, prereleases among themselves by suffix.
#
# Sourced by test-checks.sh; guarded at the bottom by PAWL_RELEASE_CHECK_TEST
# like the other release scripts.
set -euo pipefail

MARKETPLACE_URL_RE='^https://github\.com/dcferreira/agent-pawl/releases/download/v([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)/agent-pawl-plugin_([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)\.zip$'
MARKETPLACE_SHA_RE='^[0-9a-f]{64}$'

# archive_source_version SOURCE_JSON
# Echoes the version of a valid archive source (url shape, tag == zip
# version, sha256 shape); returns 1 with a reason on stdout otherwise.
archive_source_version() {
  local src="$1" kind url sha
  kind="$(jq -r 'if type == "object" then (.source // "") else "" end' <<<"$src")"
  if [ "$kind" != "archive" ]; then
    echo "source is not an archive source"
    return 1
  fi
  url="$(jq -r '.url // ""' <<<"$src")"
  sha="$(jq -r '.sha256 // ""' <<<"$src")"
  if ! [[ "$url" =~ $MARKETPLACE_URL_RE ]] || [ "${BASH_REMATCH[1]}" != "${BASH_REMATCH[3]}" ]; then
    echo "url '$url' is not https://github.com/dcferreira/agent-pawl/releases/download/vX.Y.Z/agent-pawl-plugin_X.Y.Z.zip"
    return 1
  fi
  local ver="${BASH_REMATCH[1]}"
  if ! [[ "$sha" =~ $MARKETPLACE_SHA_RE ]]; then
    echo "sha256 '$sha' is not 64 lowercase hex characters"
    return 1
  fi
  echo "$ver"
}

# _version_key VERSION -> "<base> <flag> <pre>", see check-version-consistency.sh.
_version_key() {
  case "$1" in
  *-*) echo "${1%%-*} 0 ${1#*-}" ;;
  *) echo "$1 1 " ;;
  esac
}

# version_newer A B: succeeds when A is strictly newer than B.
version_newer() {
  [ "$1" != "$2" ] || return 1
  local top
  top="$(printf '%s\n%s\n' "$(_version_key "$1")" "$(_version_key "$2")" |
    sort -k1,1V -k2,2n -k3,3V | tail -n1)"
  [ "$top" = "$(_version_key "$1")" ]
}

update_repo_marketplace() {
  local release="${1:-}" repo="${2:-.claude-plugin/marketplace.json}"
  if [ -z "$release" ]; then
    echo "usage: update-repo-marketplace.sh <release-marketplace.json> [repo-marketplace.json]" >&2
    return 2
  fi
  local f
  for f in "$release" "$repo"; do
    if [ ! -f "$f" ]; then
      echo "FAIL: $f not found" >&2
      return 1
    fi
  done

  local new_src new_ver why
  new_src="$(jq -c '.plugins[0].source // null' "$release")" || {
    echo "FAIL: $release is not valid JSON" >&2
    return 1
  }
  if ! new_ver="$(archive_source_version "$new_src")"; then
    why="$new_ver"
    echo "FAIL: $release .plugins[0].source is invalid: $why" >&2
    return 1
  fi

  local cur_src
  cur_src="$(jq -c '[.plugins[]? | select(.name == "agent-pawl")][0].source // empty' "$repo")" || {
    echo "FAIL: $repo is not valid JSON" >&2
    return 1
  }
  if ! jq -e '[.plugins[]? | select(.name == "agent-pawl")] | length > 0' "$repo" >/dev/null; then
    echo "FAIL: $repo has no plugin named agent-pawl" >&2
    return 1
  fi

  if [ "$cur_src" = "$new_src" ]; then
    echo "ok: $repo already points at v$new_ver"
    return 0
  fi
  local cur_ver
  if cur_ver="$(archive_source_version "$cur_src" 2>/dev/null)" && version_newer "$cur_ver" "$new_ver"; then
    echo "notice: $repo already points at v$cur_ver, newer than v$new_ver — not downgrading"
    return 0
  fi

  local tmp
  tmp="$(mktemp "$repo.XXXXXX")"
  if ! jq --argjson src "$new_src" \
    '(.plugins[] | select(.name == "agent-pawl") | .source) = $src' "$repo" >"$tmp"; then
    rm -f "$tmp"
    echo "FAIL: couldn't update $repo" >&2
    return 1
  fi
  chmod --reference="$repo" "$tmp" 2>/dev/null || chmod 644 "$tmp"
  mv "$tmp" "$repo"
  echo "ok: pointed $repo at v$new_ver"
}

main() {
  update_repo_marketplace "$@"
}

if [ "${PAWL_RELEASE_CHECK_TEST:-0}" != "1" ]; then
  main "$@"
fi
