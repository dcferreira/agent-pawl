#!/usr/bin/env sh
# Installs the pawl binary from a GitHub release, no Go toolchain required.
#
#   curl -fsSL https://raw.githubusercontent.com/dcferreira/agent-pawl/main/install.sh | sh
#
# Env vars:
#   PAWL_VERSION  Install a specific version (e.g. "v0.1.0" or "0.1.0")
#                 instead of the latest release.
#   INSTALL_DIR   Where to put the pawl binary. Defaults to $HOME/.local/bin.
#
# This script is written in POSIX sh (not bash) so it runs unmodified under
# whatever /bin/sh a `curl | sh` pipeline invokes, and it is deliberately
# split into small functions with no side effects (no network, no file
# writes) so scripts/test-install.sh can source this file and unit-test
# them directly, without running the installer for real. See the
# PAWL_INSTALL_TEST guard at the bottom.
set -eu

PAWL_REPO="dcferreira/agent-pawl"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# ---------------------------------------------------------------------------
# Pure logic: OS/arch detection, asset naming, version handling, checksum
# parsing. No network calls, no filesystem writes. Unit-tested in
# scripts/test-install.sh.
# ---------------------------------------------------------------------------

# pawl_os_from_uname maps `uname -s` output to a goreleaser GOOS string, or
# fails with an actionable message for anything this project doesn't ship
# binaries for (e.g. Windows' MINGW64_NT-* from Git Bash).
pawl_os_from_uname() {
  case "$1" in
    Darwin) echo darwin ;;
    Linux) echo linux ;;
    *)
      echo "error: unsupported OS '$1'. pawl release binaries are only published for Linux and macOS (Darwin)." >&2
      echo "       Build from source instead: see docs/install.md." >&2
      return 1
      ;;
  esac
}

# pawl_arch_from_uname maps `uname -m` output to a goreleaser GOARCH
# string, or fails with an actionable message (e.g. 32-bit x86).
pawl_arch_from_uname() {
  case "$1" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *)
      echo "error: unsupported architecture '$1'. pawl release binaries are only published for amd64 (x86_64) and arm64 (aarch64)." >&2
      echo "       Build from source instead: see docs/install.md." >&2
      return 1
      ;;
  esac
}

pawl_detect_os() {
  pawl_os_from_uname "$(uname -s)"
}

pawl_detect_arch() {
  pawl_arch_from_uname "$(uname -m)"
}

# pawl_asset_name builds the release archive filename. Must match
# .goreleaser.yaml's archives.name_template exactly:
# pawl_<version-without-v>_<os>_<arch>.tar.gz
pawl_asset_name() {
  version_no_v="$1"
  os="$2"
  arch="$3"
  printf 'pawl_%s_%s_%s.tar.gz\n' "$version_no_v" "$os" "$arch"
}

# pawl_normalize_version ensures a version string has a leading "v", as
# GitHub release tags do.
pawl_normalize_version() {
  case "$1" in
    v*) printf '%s\n' "$1" ;;
    *) printf 'v%s\n' "$1" ;;
  esac
}

# pawl_version_no_v strips a leading "v", matching goreleaser's
# {{.Version}} template value used in archive names.
pawl_version_no_v() {
  case "$1" in
    v*) printf '%s\n' "${1#v}" ;;
    *) printf '%s\n' "$1" ;;
  esac
}

# pawl_latest_version_from_json extracts "tag_name" from a GitHub
# releases/latest API JSON response. Parsed with grep/sed rather than jq:
# jq is not guaranteed to be installed on a machine running a `curl | sh`
# one-liner, and this is the only field this script needs out of the
# response.
pawl_latest_version_from_json() {
  printf '%s\n' "$1" |
    grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' |
    head -n1 |
    sed -E 's/.*"([^"]+)"$/\1/'
}

# pawl_checksum_for_file looks up the sha256 for a given asset filename in
# a checksums.txt file's contents (goreleaser's default `checksum` block
# format: "<sha256>  <filename>" per line). Fails if the filename isn't
# present.
pawl_checksum_for_file() {
  content="$1"
  filename="$2"
  printf '%s\n' "$content" | awk -v f="$filename" '$2 == f { print $1; found=1 } END { exit(found ? 0 : 1) }'
}

# ---------------------------------------------------------------------------
# Effectful helpers: network, filesystem, environment. Not unit-tested
# directly (that would mean the tests hit the network); exercised via the
# real installer run in CI/manual verification instead.
# ---------------------------------------------------------------------------

pawl_have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

# pawl_fetch downloads $1 to stdout, using curl if available and falling
# back to wget (one of the two is present on essentially every Linux/macOS
# system this script targets).
pawl_fetch() {
  url="$1"
  if pawl_have_cmd curl; then
    curl -fsSL "$url"
  elif pawl_have_cmd wget; then
    wget -qO- "$url"
  else
    echo "error: neither curl nor wget is available; cannot download pawl." >&2
    return 1
  fi
}

# pawl_fetch_to_file downloads $1 to file $2.
pawl_fetch_to_file() {
  url="$1"
  dest="$2"
  if pawl_have_cmd curl; then
    curl -fsSL -o "$dest" "$url"
  elif pawl_have_cmd wget; then
    wget -qO "$dest" "$url"
  else
    echo "error: neither curl nor wget is available; cannot download pawl." >&2
    return 1
  fi
}

# pawl_sha256 computes the sha256 of file $1, using whichever of
# sha256sum (GNU/most Linux) or shasum -a 256 (macOS, and Linux systems
# without coreutils' sha256sum) is available.
pawl_sha256() {
  file="$1"
  if pawl_have_cmd sha256sum; then
    sha256sum "$file" | awk '{print $1}'
  elif pawl_have_cmd shasum; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    echo "error: neither sha256sum nor shasum is available; cannot verify the download." >&2
    return 1
  fi
}

# pawl_resolve_version echoes the release tag to install: $PAWL_VERSION if
# set (normalized to have a "v" prefix), otherwise the latest release's
# tag from the GitHub API.
pawl_resolve_version() {
  if [ -n "${PAWL_VERSION:-}" ]; then
    pawl_normalize_version "$PAWL_VERSION"
    return
  fi
  json="$(pawl_fetch "https://api.github.com/repos/${PAWL_REPO}/releases/latest")"
  tag="$(pawl_latest_version_from_json "$json")"
  if [ -z "$tag" ]; then
    echo "error: could not determine the latest pawl release (GitHub API response had no tag_name)." >&2
    return 1
  fi
  printf '%s\n' "$tag"
}

# pawl_path_has_dir reports (via exit code) whether $1 is already an entry
# of $PATH.
pawl_path_has_dir() {
  dir="$1"
  case ":${PATH:-}:" in
    *":${dir}:"*) return 0 ;;
    *) return 1 ;;
  esac
}

main() {
  os="$(pawl_detect_os)"
  arch="$(pawl_detect_arch)"

  version_tag="$(pawl_resolve_version)"
  version_no_v="$(pawl_version_no_v "$version_tag")"
  asset="$(pawl_asset_name "$version_no_v" "$os" "$arch")"

  base_url="https://github.com/${PAWL_REPO}/releases/download/${version_tag}"
  archive_url="${base_url}/${asset}"
  checksums_url="${base_url}/checksums.txt"

  echo "Installing pawl ${version_tag} (${os}/${arch})..."

  workdir="$(mktemp -d)"
  trap 'rm -rf "$workdir"' EXIT

  echo "Downloading ${archive_url}"
  pawl_fetch_to_file "$archive_url" "${workdir}/${asset}"

  echo "Downloading ${checksums_url}"
  checksums_content="$(pawl_fetch "$checksums_url")"

  expected_sum="$(pawl_checksum_for_file "$checksums_content" "$asset")" || {
    echo "error: no checksum found for ${asset} in checksums.txt" >&2
    exit 1
  }
  actual_sum="$(pawl_sha256 "${workdir}/${asset}")"
  if [ "$expected_sum" != "$actual_sum" ]; then
    echo "error: checksum mismatch for ${asset}" >&2
    echo "       expected: ${expected_sum}" >&2
    echo "       actual:   ${actual_sum}" >&2
    exit 1
  fi
  echo "Checksum OK."

  tar -xzf "${workdir}/${asset}" -C "$workdir" pawl

  mkdir -p "$INSTALL_DIR"
  install -m 0755 "${workdir}/pawl" "${INSTALL_DIR}/pawl" 2>/dev/null ||
    { cp "${workdir}/pawl" "${INSTALL_DIR}/pawl" && chmod +x "${INSTALL_DIR}/pawl"; }

  echo "Installed ${INSTALL_DIR}/pawl"
  "${INSTALL_DIR}/pawl" version || true

  if ! pawl_path_has_dir "$INSTALL_DIR"; then
    echo
    echo "NOTE: ${INSTALL_DIR} is not on your PATH. Add it, e.g.:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
  fi
}

# Sourcing this file for tests (scripts/test-install.sh sets
# PAWL_INSTALL_TEST=1 first) must not run the real installer.
if [ "${PAWL_INSTALL_TEST:-0}" != "1" ]; then
  main "$@"
fi
