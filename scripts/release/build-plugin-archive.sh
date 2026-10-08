#!/usr/bin/env bash
# Builds the two extra release assets the plugin install path needs, from a
# goreleaser dist/ directory:
#
#   agent-pawl-plugin_<ver>.zip   the plugin root: .claude-plugin/plugin.json,
#                                 hooks/hooks.json, skills/pawl/SKILL.md,
#                                 bin/pawl, bin/pawl-hook and
#                                 libexec/<os>_<arch>/pawl for
#                                 os in darwin,linux x arch in amd64,arm64
#   marketplace.json              a Claude Code marketplace whose one plugin
#                                 is an `archive` source pointing at the zip's
#                                 GitHub release URL, pinned by its sha256
#
# Usage: build-plugin-archive.sh <version> [dist-dir] [out-dir]
#   version   X.Y.Z (a leading "v" is stripped, so release.yml can pass its
#             VERSION, which is the tag name, as-is)
#   dist-dir  goreleaser output, default ./dist (pawl_<ver>_<os>_<arch>.tar.gz
#             plus checksums.txt)
#   out-dir   default <dist-dir>/plugin
# Env: PLUGIN_SRC  the plugin source tree (default: this repo's root).
#
# Every tarball is verified against checksums.txt BEFORE anything is
# unpacked, and a missing archive, a missing checksum line or a mismatch is a
# hard failure — the binaries inside end up executed by hooks, so they are
# never trusted on name alone. plugin.json's version must equal <ver>: the
# plugin is versioned (and auto-updated) by that field, so a zip whose
# plugin.json disagrees with its release tag would never be detected as an
# update, or would be detected forever.
#
# Built with `zip -X` from an explicit file list so modes survive and nothing
# else from the tree leaks in; skills/pawl is resolved to real files (the
# repo's .claude/skills/pawl is a symlink to it, and a zip must not carry
# symlinks).
#
# Sourced by test-checks.sh; guarded at the bottom by PAWL_RELEASE_CHECK_TEST
# like the other release scripts.
set -euo pipefail

PLUGIN_OSES="darwin linux"
PLUGIN_ARCHES="amd64 arm64"
PLUGIN_REPO_URL="https://github.com/dcferreira/agent-pawl"

_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# verify_archive DIST_DIR ARCHIVE_NAME
verify_archive() {
  local dist="$1" name="$2" want got
  if [ ! -f "$dist/$name" ]; then
    echo "FAIL: missing archive $dist/$name" >&2
    return 1
  fi
  if [ ! -f "$dist/checksums.txt" ]; then
    echo "FAIL: missing $dist/checksums.txt" >&2
    return 1
  fi
  want="$(awk -v n="$name" '$2 == n || $2 == "*" n {print $1; exit}' "$dist/checksums.txt")"
  if [ -z "$want" ]; then
    echo "FAIL: no checksum line for $name in $dist/checksums.txt" >&2
    return 1
  fi
  got="$(_sha256 "$dist/$name")"
  if [ "$got" != "$want" ]; then
    echo "FAIL: checksum mismatch for $name: checksums.txt says $want, file is $got" >&2
    return 1
  fi
}

build_plugin_archive() {
  local version="${1:-}" dist="${2:-./dist}"
  local out="${3:-}"
  local src="${PLUGIN_SRC:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"

  if [ -z "$version" ]; then
    echo "usage: build-plugin-archive.sh <version> [dist-dir] [out-dir]" >&2
    return 2
  fi
  version="${version#v}"
  case "$version" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo "FAIL: version '$version' doesn't look like X.Y.Z" >&2
    return 1
    ;;
  esac
  [ -n "$out" ] || out="$dist/plugin"

  local pj_ver
  pj_ver="$(jq -r '.version // empty' "$src/.claude-plugin/plugin.json")" || return 1
  if [ "$pj_ver" != "$version" ]; then
    echo "FAIL: $src/.claude-plugin/plugin.json version ($pj_ver) != release version ($version)" >&2
    return 1
  fi

  local f
  for f in .claude-plugin/plugin.json hooks/hooks.json skills/pawl/SKILL.md bin/pawl bin/pawl-hook; do
    if [ ! -f "$src/$f" ]; then
      echo "FAIL: plugin source is missing $src/$f" >&2
      return 1
    fi
  done

  local os arch plat
  # Verify all four before unpacking any.
  for os in $PLUGIN_OSES; do
    for arch in $PLUGIN_ARCHES; do
      verify_archive "$dist" "pawl_${version}_${os}_${arch}.tar.gz" || return 1
    done
  done

  local stage
  stage="$(mktemp -d)" || return 1
  # shellcheck disable=SC2064
  trap "rm -rf '$stage'" RETURN

  mkdir -p "$stage/.claude-plugin" "$stage/hooks" "$stage/skills/pawl" "$stage/bin"
  cp -L "$src/.claude-plugin/plugin.json" "$stage/.claude-plugin/plugin.json"
  cp -L "$src/hooks/hooks.json" "$stage/hooks/hooks.json"
  cp -L "$src/skills/pawl/SKILL.md" "$stage/skills/pawl/SKILL.md"
  cp -L "$src/bin/pawl" "$src/bin/pawl-hook" "$stage/bin/"
  chmod 0644 "$stage/.claude-plugin/plugin.json" "$stage/hooks/hooks.json" "$stage/skills/pawl/SKILL.md"
  chmod 0755 "$stage/bin/pawl" "$stage/bin/pawl-hook"

  for os in $PLUGIN_OSES; do
    for arch in $PLUGIN_ARCHES; do
      plat="${os}_${arch}"
      mkdir -p "$stage/libexec/$plat"
      tar -xzf "$dist/pawl_${version}_${plat}.tar.gz" -C "$stage/libexec/$plat" pawl || {
        echo "FAIL: no 'pawl' binary in pawl_${version}_${plat}.tar.gz" >&2
        return 1
      }
      chmod 0755 "$stage/libexec/$plat/pawl"
    done
  done

  mkdir -p "$out"
  local zip_abs
  zip_abs="$(cd "$out" && pwd)/agent-pawl-plugin_${version}.zip"
  rm -f "$zip_abs"
  (
    cd "$stage"
    zip -X -q "$zip_abs" \
      .claude-plugin/plugin.json hooks/hooks.json skills/pawl/SKILL.md \
      bin/pawl bin/pawl-hook \
      libexec/darwin_amd64/pawl libexec/darwin_arm64/pawl \
      libexec/linux_amd64/pawl libexec/linux_arm64/pawl
  ) || return 1

  local sha url
  sha="$(_sha256 "$zip_abs")"
  url="$PLUGIN_REPO_URL/releases/download/v${version}/agent-pawl-plugin_${version}.zip"
  jq -n --arg url "$url" --arg sha "$sha" '{
    name: "agent-pawl",
    owner: {name: "Daniel Ferreira"},
    description: "The agent-pawl plugin: a Claude Code skill and hooks for driving the pawl workflow engine, shipping the pawl binary.",
    plugins: [{
      name: "agent-pawl",
      description: "Drive pawl workflow runs from Claude Code; bundles the pawl binary for darwin and linux (amd64, arm64).",
      source: {source: "archive", url: $url, sha256: $sha}
    }]
  }' >"$out/marketplace.json" || return 1

  echo "ok: wrote $zip_abs (sha256 $sha) and $out/marketplace.json"
}

main() {
  build_plugin_archive "$@"
}

if [ "${PAWL_RELEASE_CHECK_TEST:-0}" != "1" ]; then
  main "$@"
fi
