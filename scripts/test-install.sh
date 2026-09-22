#!/usr/bin/env bash
# Unit tests for install.sh's pure logic (OS/arch detection, asset naming,
# version resolution, checksum-line parsing). Deliberately dependency-free
# (no bats) to match this repo's no-extra-tooling ethos: plain assertions,
# run with `make test-install` or `bash scripts/test-install.sh`.
#
# install.sh is SOURCED, not executed, so its functions become callable
# here without running the real installer (network calls, file writes).
# install.sh checks PAWL_INSTALL_TEST before running main() at the bottom
# of the file for exactly this reason.
set -euo pipefail

cd "$(dirname "$0")/.."

export PAWL_INSTALL_TEST=1
# shellcheck source=/dev/null
source ./install.sh

failures=0
tests_run=0

# assert_eq DESCRIPTION EXPECTED ACTUAL
assert_eq() {
  tests_run=$((tests_run + 1))
  local desc="$1" expected="$2" actual="$3"
  if [ "$expected" != "$actual" ]; then
    echo "FAIL: $desc"
    echo "  expected: $expected"
    echo "  actual:   $actual"
    failures=$((failures + 1))
  else
    echo "ok: $desc"
  fi
}

# assert_fail DESCRIPTION -- COMMAND...
# Asserts that running the given command exits non-zero.
assert_fail() {
  tests_run=$((tests_run + 1))
  local desc="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    echo "FAIL: $desc (expected failure, got success)"
    failures=$((failures + 1))
  else
    echo "ok: $desc"
  fi
}

echo "== pawl_os_from_uname =="
assert_eq "Darwin -> darwin" "darwin" "$(pawl_os_from_uname Darwin)"
assert_eq "Linux -> linux" "linux" "$(pawl_os_from_uname Linux)"
assert_fail "Windows-ish uname rejected" pawl_os_from_uname "MINGW64_NT-10.0"
assert_fail "empty uname rejected" pawl_os_from_uname ""

echo "== pawl_arch_from_uname =="
assert_eq "x86_64 -> amd64" "amd64" "$(pawl_arch_from_uname x86_64)"
assert_eq "amd64 -> amd64" "amd64" "$(pawl_arch_from_uname amd64)"
assert_eq "aarch64 -> arm64" "arm64" "$(pawl_arch_from_uname aarch64)"
assert_eq "arm64 -> arm64" "arm64" "$(pawl_arch_from_uname arm64)"
assert_fail "386 rejected" pawl_arch_from_uname "i386"
assert_fail "empty arch rejected" pawl_arch_from_uname ""

echo "== pawl_asset_name =="
assert_eq "asset name" "pawl_1.2.3_linux_amd64.tar.gz" "$(pawl_asset_name "1.2.3" "linux" "amd64")"
assert_eq "asset name darwin arm64" "pawl_0.1.0_darwin_arm64.tar.gz" "$(pawl_asset_name "0.1.0" "darwin" "arm64")"

echo "== pawl_normalize_version / pawl_version_no_v =="
assert_eq "normalize adds v" "v1.2.3" "$(pawl_normalize_version "1.2.3")"
assert_eq "normalize keeps v" "v1.2.3" "$(pawl_normalize_version "v1.2.3")"
assert_eq "strip v" "1.2.3" "$(pawl_version_no_v "v1.2.3")"
assert_eq "strip v noop" "1.2.3" "$(pawl_version_no_v "1.2.3")"

echo "== pawl_latest_version_from_json =="
sample_json='{"url":"https://api.github.com/repos/dcferreira/agent-pawl/releases/123","tag_name":"v0.3.1","name":"v0.3.1","draft":false}'
assert_eq "extract tag_name" "v0.3.1" "$(pawl_latest_version_from_json "$sample_json")"

echo "== pawl_checksum_for_file =="
sample_checksums='dbefa70d50684f84a30402855cb3d5f755fc84a171e22ec3336a08e5ec7a2986  pawl_0.1.0_darwin_amd64.tar.gz
df859ba274b8d125eb5868cdf36f95c54d2b3ece809df391a2432f634eb25e2d  pawl_0.1.0_darwin_arm64.tar.gz
5b88b6599168a6dfd0fc9f3ba02523c8b4d618a63c6a5cb25921daba1d6ea52c  pawl_0.1.0_linux_amd64.tar.gz'
assert_eq "checksum lookup: linux amd64" \
  "5b88b6599168a6dfd0fc9f3ba02523c8b4d618a63c6a5cb25921daba1d6ea52c" \
  "$(pawl_checksum_for_file "$sample_checksums" "pawl_0.1.0_linux_amd64.tar.gz")"
assert_eq "checksum lookup: darwin arm64" \
  "df859ba274b8d125eb5868cdf36f95c54d2b3ece809df391a2432f634eb25e2d" \
  "$(pawl_checksum_for_file "$sample_checksums" "pawl_0.1.0_darwin_arm64.tar.gz")"
assert_fail "checksum lookup: missing asset fails" pawl_checksum_for_file "$sample_checksums" "pawl_0.1.0_windows_amd64.tar.gz"

echo
echo "$tests_run tests run, $failures failed"
if [ "$failures" -ne 0 ]; then
  exit 1
fi
