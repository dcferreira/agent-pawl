#!/usr/bin/env bash
# Scenario B: terminal installs - install.sh (latest + pinned FROM), go install,
# `pawl update` FROM->TO on an install.sh binary, and `pawl update` refusing on
# a plugin-managed binary.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
# shellcheck source-path=SCRIPTDIR
. "$HERE/lib.sh"

e2e_init b
P="$(plat)"

# install.sh from the TO tag. It honours INSTALL_DIR and PAWL_VERSION.
inst="$ROOT/install-$TO.sh"
step "download install.sh at v$TO" fetch "https://raw.githubusercontent.com/$REPO_SLUG/v$TO/install.sh" "$inst"
[ "$STEP_RC" -eq 0 ] || { check FAIL "download install.sh at v$TO" "exit $STEP_RC"; e2e_finish; exit $?; }

# run_install <dir> <pinned-version-or-empty>; sets STEP_*.
run_install() {
  local dir="$1" pin="$2"
  if [ -n "$pin" ]; then
    step "install.sh PAWL_VERSION=v$pin -> $dir" sb "INSTALL_DIR=$dir" "PAWL_VERSION=v$pin" sh "$inst"
  else
    step "install.sh (latest) -> $dir" sb "INSTALL_DIR=$dir" sh "$inst"
  fi
}

verify_install() { # <label> <dir> <want-version>
  local label="$1" dir="$2" want="$3" got
  got="$(version_of "$dir/pawl")"
  if [ "$STEP_RC" -eq 0 ] && [ "$got" = "$want" ]; then
    check PASS "$label: binary reports $want" "$dir/pawl -> $got"
  else
    check FAIL "$label: binary reports $want" "rc=$STEP_RC got='$got' err=$STEP_ERR"
  fi
  if printf '%s\n' "$STEP_OUT" | grep -q '^Checksum OK\.$'; then
    check PASS "$label: checksum verified" "install.sh output line: Checksum OK."
  else
    check FAIL "$label: checksum verified" "no 'Checksum OK.' line in install.sh output"
  fi
  case "$(readlink -f "$dir/pawl")" in
    "$ROOT"/*) ;;
    *) check FAIL "$label: installed inside sandbox" "$dir/pawl" ;;
  esac
}

# 1. latest (live) or pinned TO (local/rc: an rc is never "latest")
if [ "$SOURCE" = live ]; then
  run_install "$ROOT/inst-latest" ""
  verify_install "install.sh latest" "$ROOT/inst-latest" "$TO"
else
  check SKIP "install.sh latest" "SOURCE=local (rc): an rc is not the GitHub latest; pinned v$TO installed instead"
  run_install "$ROOT/inst-latest" "$TO"
  verify_install "install.sh pinned TO" "$ROOT/inst-latest" "$TO"
fi

# 2. pinned FROM
run_install "$ROOT/inst-from" "$FROM"
verify_install "install.sh pinned $FROM" "$ROOT/inst-from" "$FROM"

# 3. tampered checksum
check SKIP "tampered checksum is rejected" "not testable without a base-URL override (install.sh hard-codes the GitHub release URL)"

# 4. go install
step "go install github.com/$REPO_SLUG/cmd/pawl@v$TO" sb go install "github.com/$REPO_SLUG/cmd/pawl@v$TO"
if [ "$STEP_RC" -eq 0 ] && [ -x "$SB_GOPATH/bin/pawl" ]; then
  step "go-installed pawl version" sb "$SB_GOPATH/bin/pawl" version
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "go install pawl@v$TO runs" "observed: $STEP_OUT"
else
  check FAIL "go install pawl@v$TO runs" "exit $STEP_RC: $STEP_ERR"
fi

# 5. pawl update FROM -> TO on the install.sh FROM binary
step "pawl update --check (from $FROM)" sb "$ROOT/inst-from/pawl" update --check
if [ "$SOURCE" = live ]; then
  upd=(update)
else
  upd=(update --version "v$TO")
fi
step "pawl ${upd[*]} on install.sh-installed $FROM binary" sb "$ROOT/inst-from/pawl" "${upd[@]}"
upd_rc="$STEP_RC"
after="$(version_of "$ROOT/inst-from/pawl")"
if [ "$upd_rc" -eq 0 ] && [ "$after" = "$TO" ]; then
  check PASS "pawl update $FROM -> $TO at the same path" "$ROOT/inst-from/pawl now reports $after"
else
  check FAIL "pawl update $FROM -> $TO at the same path" "rc=$upd_rc now reports '$after'"
fi

# 6. pawl update on a plugin-managed binary must refuse
if install_plugin; then
  real="$PLUGIN_DIR/libexec/$P/pawl"
  before_sum="$(sha256sum "$real" | awk '{print $1}')"
  step "pawl update on libexec binary" sb "$real" update
  rc1="$STEP_RC"
  msg1="$STEP_ERR"
  step "pawl update via bin/pawl launcher" sb "$PLUGIN_DIR/bin/pawl" update
  rc2="$STEP_RC"
  msg2="$STEP_ERR"
  after_sum="$(sha256sum "$real" | awk '{print $1}')"
  for pair in "libexec binary:$rc1:$msg1" "bin/pawl launcher:$rc2:$msg2"; do
    label="${pair%%:*}"
    rest="${pair#*:}"
    rc="${rest%%:*}"
    msg="${rest#*:}"
    if [ "$rc" -ne 0 ] && printf '%s' "$msg" | grep -q 'claude plugin update'; then
      check PASS "pawl update refused on plugin-managed $label" "exit $rc, names 'claude plugin update'"
    else
      check FAIL "pawl update refused on plugin-managed $label" "exit $rc stderr: $msg"
    fi
  done
  if [ "$before_sum" = "$after_sum" ]; then
    check PASS "plugin-managed binary unchanged after refused update" "sha256 $after_sum"
  else
    check FAIL "plugin-managed binary unchanged after refused update" "before=$before_sum after=$after_sum"
  fi
fi

e2e_finish
