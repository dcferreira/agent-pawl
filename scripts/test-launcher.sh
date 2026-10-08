#!/usr/bin/env bash
# Tests for bin/pawl, the plugin's launcher: it must exec the bundled
# libexec/<os>_<arch>/pawl when the plugin ships one, fail loudly (no silent
# PATH fallback) when the bundle exists but lacks this platform, and only fall
# back to a PATH search in a checkout that has no libexec/ at all.
#
# Plain bash, fixtures in mktemp -d, no network. `uname` is stubbed through a
# PATH directory so every platform branch is testable on any host.
# Run with `make test-launcher` or `bash scripts/test-launcher.sh`.
set -uo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd -P)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# A hermetic tool directory: only what the launcher needs, so a real `pawl`
# installed on the host can never leak into the PATH-fallback assertions.
tools="$tmp/tools"
mkdir -p "$tools"
for t in bash env dirname basename readlink cat timeout; do
  if p="$(command -v "$t")" && [ -x "$p" ]; then ln -s "$p" "$tools/$t"; fi
done

TIMEOUT_CMD=""
[ -e "$tools/timeout" ] && TIMEOUT_CMD="$tools/timeout 10"
failures=0
tests_run=0

pass() { echo "ok: $1"; }
fail() { echo "FAIL: $1"; [ -n "${2:-}" ] && echo "  $2"; failures=$((failures + 1)); }

# check DESCRIPTION CONDITION-EXIT-CODE [DETAIL]
check() {
  tests_run=$((tests_run + 1))
  if [ "$2" -eq 0 ]; then pass "$1"; else fail "$1" "${3:-}"; fi
}

contains() { case "$1" in *"$2"*) return 0 ;; *) return 1 ;; esac; }

# new_plugin NAME -> prints the root of a fresh plugin tree holding the real launcher.
new_plugin() {
  local root="$tmp/$1"
  mkdir -p "$root/bin" "$root/.claude-plugin"
  cp "$repo/bin/pawl" "$root/bin/pawl"
  chmod 755 "$root/bin/pawl"
  echo "$root"
}

# stub_uname DIR OS ARCH: a fake `uname` answering -s and -m.
stub_uname() {
  mkdir -p "$1"
  cat > "$1/uname" <<STUB
#!/bin/sh
case "\$1" in
  -s) echo "$2" ;;
  -m) echo "$3" ;;
  *) echo "$2" ;;
esac
STUB
  chmod 755 "$1/uname"
}

# bundle ROOT PLATFORM: a fake bundled binary that reports its args.
bundle() {
  mkdir -p "$1/libexec/$2"
  cat > "$1/libexec/$2/pawl" <<'STUB'
#!/bin/sh
echo "bundled:$*"
exit "${PAWL_TEST_EXIT:-0}"
STUB
  chmod 755 "$1/libexec/$2/pawl"
}

# path_pawl DIR: a fake `pawl` on PATH.
path_pawl() {
  mkdir -p "$1"
  printf '#!/bin/sh\necho "path:$*"\n' > "$1/pawl"
  chmod 755 "$1/pawl"
}

echo "== bundled binary =="
root="$(new_plugin bundled)"
bundle "$root" linux_amd64
stub_uname "$tmp/u-linux-amd64" Linux x86_64
out="$(PATH="$tmp/u-linux-amd64:$tools" "$root/bin/pawl" run --flag value 2>&1)"
check "bundled binary is exec'd with its args" "$([ "$out" = "bundled:run --flag value" ]; echo $?)" "got: $out"
PATH="$tmp/u-linux-amd64:$tools" PAWL_TEST_EXIT=7 "$root/bin/pawl" x >/dev/null 2>&1
code=$?
check "bundled binary's exit code passes through" "$([ "$code" -eq 7 ]; echo $?)" "got: $code"

# A PATH pawl must never win over the bundle.
path_pawl "$tmp/pathbin"
out="$(PATH="$tmp/pathbin:$tmp/u-linux-amd64:$tools" "$root/bin/pawl" a 2>&1)"
check "bundle wins over a pawl on PATH" "$([ "$out" = "bundled:a" ]; echo $?)" "got: $out"

mkdir -p "$tmp/elsewhere"
ln -s "$root/bin/pawl" "$tmp/elsewhere/pawl"
out="$(PATH="$tmp/u-linux-amd64:$tools" "$tmp/elsewhere/pawl" via-symlink 2>&1)"
check "symlinked invocation still finds the bundle" "$([ "$out" = "bundled:via-symlink" ]; echo $?)" "got: $out"

mkdir -p "$tmp/elsewhere2"
ln -s "$tmp/elsewhere/pawl" "$tmp/elsewhere2/pawl"
out="$(PATH="$tmp/u-linux-amd64:$tools" "$tmp/elsewhere2/pawl" chain 2>&1)"
check "chained symlinks still find the bundle" "$([ "$out" = "bundled:chain" ]; echo $?)" "got: $out"

# Relative symlink target.
mkdir -p "$tmp/rel"
(cd "$tmp/rel" && ln -s ../bundled/bin/pawl pawl)
out="$(PATH="$tmp/u-linux-amd64:$tools" "$tmp/rel/pawl" rel 2>&1)"
check "relative symlink still finds the bundle" "$([ "$out" = "bundled:rel" ]; echo $?)" "got: $out"

echo "== platform mapping =="
for case in "Darwin arm64 darwin_arm64" "Darwin x86_64 darwin_amd64" "Linux aarch64 linux_arm64" "Linux x86_64 linux_amd64"; do
  set -- $case
  r="$(new_plugin "map-$3")"
  bundle "$r" "$3"
  stub_uname "$tmp/u-$3" "$1" "$2"
  out="$(PATH="$tmp/u-$3:$tools" "$r/bin/pawl" m 2>&1)"
  check "$1/$2 -> libexec/$3" "$([ "$out" = "bundled:m" ]; echo $?)" "got: $out"
done

echo "== unsupported platform =="
r="$(new_plugin unsupported-arch)"
bundle "$r" linux_amd64
stub_uname "$tmp/u-386" Linux i686
out="$(PATH="$tmp/u-386:$tools" "$r/bin/pawl" x 2>&1)"
code=$?
check "unsupported arch exits non-zero" "$([ "$code" -ne 0 ]; echo $?)" "code: $code"
check "unsupported arch message names the platform" "$(contains "$out" "i686"; echo $?)" "got: $out"
check "unsupported arch message mentions unsupported" "$(contains "$out" "unsupported"; echo $?)" "got: $out"

stub_uname "$tmp/u-win" "MINGW64_NT-10.0" x86_64
out="$(PATH="$tmp/u-win:$tools" "$r/bin/pawl" x 2>&1)"
code=$?
check "unsupported OS exits non-zero" "$([ "$code" -ne 0 ]; echo $?)" "code: $code"
check "unsupported OS message names the OS" "$(contains "$out" "MINGW64_NT-10.0"; echo $?)" "got: $out"

echo "== libexec present, platform binary missing =="
r="$(new_plugin missing-platform)"
bundle "$r" darwin_arm64
path_pawl "$tmp/pathbin2"
out="$(PATH="$tmp/pathbin2:$tmp/u-linux-amd64:$tools" "$r/bin/pawl" x 2>&1)"
code=$?
check "missing platform binary exits non-zero" "$([ "$code" -ne 0 ]; echo $?)" "code: $code"
check "missing platform binary does NOT fall back to PATH" "$(contains "$out" "path:"; [ $? -ne 0 ]; echo $?)" "got: $out"
check "missing platform binary names linux_amd64" "$(contains "$out" "linux_amd64"; echo $?)" "got: $out"

r="$(new_plugin not-executable)"
bundle "$r" linux_amd64
chmod 644 "$r/libexec/linux_amd64/pawl"
out="$(PATH="$tmp/pathbin2:$tmp/u-linux-amd64:$tools" "$r/bin/pawl" x 2>&1)"
code=$?
check "non-executable bundled binary fails, no PATH fallback" "$([ "$code" -ne 0 ] && ! contains "$out" "path:"; echo $?)" "got: $out"

echo "== no libexec: PATH fallback =="
r="$(new_plugin checkout)"
out="$(PATH="$tmp/pathbin:$tools" "$r/bin/pawl" fb arg 2>&1)"
check "no libexec falls back to pawl on PATH" "$([ "$out" = "path:fb arg" ]; echo $?)" "got: $out"

# Self-pointing symlink in a PATH dir: must be skipped, not recursed into.
mkdir -p "$tmp/selfbin" "$tmp/emptybin"
ln -s "$r/bin/pawl" "$tmp/selfbin/pawl"
out="$(PATH="$tmp/selfbin:$tools" ${TIMEOUT_CMD:-} "$r/bin/pawl" x 2>&1)"
code=$?
check "self-pointing PATH symlink is not recursed into" "$([ "$code" -eq 1 ] && ! contains "$out" "re-entered"; echo $?)" "code: $code got: $out"
check "fallback error says this checkout has no bundled binary" "$(contains "$out" "no bundled"; echo $?)" "got: $out"
check "fallback error points at the release marketplace" "$(contains "$out" "releases/latest/download/marketplace.json"; echo $?)" "got: $out"
check "fallback error points at go install / install.sh" "$(contains "$out" "go install"; echo $?)" "got: $out"
check "fallback error no longer claims a plugin cannot ship the binary" "$(contains "$out" "cannot ship"; [ $? -ne 0 ]; echo $?)" "got: $out"

# Re-entry guard still works as a last line of defence.
out="$(PAWL_WRAPPER_ACTIVE=1 "$r/bin/pawl" x 2>&1)"
code=$?
check "re-entry guard refuses to recurse" "$([ "$code" -eq 1 ] && contains "$out" "re-entered"; echo $?)" "got: $out"

echo "== pawl-hook calls the sibling launcher =="
hr="$(new_plugin hookroot)"
cp "$repo/bin/pawl-hook" "$hr/bin/pawl-hook"
chmod 755 "$hr/bin/pawl-hook"
bundle "$hr" linux_amd64
out="$(printf '{"x":"pawl"}' | PATH="$tmp/u-linux-amd64:$tools" PAWL_STATE_DIR="$tmp/state/runs" "$hr/bin/pawl-hook" pre 2>&1)"
code=$?
check "pawl-hook reaches the bundled binary and passes its output" "$([ "$code" -eq 0 ] && [ "$out" = "bundled:hook pre" ]; echo $?)" "code: $code got: $out"
out="$(printf '{"x":"pawl"}' | PATH="$tmp/u-linux-amd64:$tools" PAWL_TEST_EXIT=2 PAWL_STATE_DIR="$tmp/state/runs" "$hr/bin/pawl-hook" pre 2>&1 >/dev/null)"
code=$?
check "pawl-hook fails open (exit 1) when the binary exits 2" "$([ "$code" -eq 1 ]; echo $?)" "code: $code"

echo
echo "$tests_run checks, $failures failure(s)"
[ "$failures" -eq 0 ]
