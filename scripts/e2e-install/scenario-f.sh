#!/usr/bin/env bash
# Scenario F: plugin/binary version-mismatch UX. A source checkout used as a
# plugin dir (no libexec/) falls back to a `pawl` on PATH; this scenario
# records what happens when that PATH binary is the wrong version.
#   F1 (realistic): TO checkout (plugin.json TO) + the FROM release binary on PATH.
#   F2 (realistic): FROM checkout (plugin.json FROM) + the TO release binary on PATH.
#   F3/F4 (synthetic): TO checkout whose plugin.json is edited to a different
#       version, with the TO binary on PATH, to exercise the check that exists
#       in TO.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
# shellcheck source-path=SCRIPTDIR
. "$HERE/lib.sh"

e2e_init f
WF=trivial

fetch_source_tree "$TO" "$ROOT/co-to" || { check FAIL "fetch v$TO source tree" "see transcript"; e2e_finish; exit $?; }
fetch_source_tree "$FROM" "$ROOT/co-from" || { check FAIL "fetch v$FROM source tree" "see transcript"; e2e_finish; exit $?; }
mkdir -p "$ROOT/frombin" "$ROOT/tobin"
fetch_release_binary "$FROM" "$ROOT/frombin/pawl" || { check FAIL "fetch $FROM release binary" "see transcript"; e2e_finish; exit $?; }
fetch_release_binary "$TO" "$ROOT/tobin/pawl" || { check FAIL "fetch $TO release binary" "see transcript"; e2e_finish; exit $?; }
if [ "$(version_of "$ROOT/frombin/pawl")" = "$FROM" ]; then
  check PASS "FROM release binary reports $FROM" "$FROM"
else
  check FAIL "FROM release binary reports $FROM" "$(version_of "$ROOT/frombin/pawl")"
fi
if [ "$(version_of "$ROOT/tobin/pawl")" = "$TO" ]; then
  check PASS "TO release binary reports $TO" "$TO"
else
  check FAIL "TO release binary reports $TO" "$(version_of "$ROOT/tobin/pawl")"
fi

for pair in "TO:$ROOT/co-to:$TO" "FROM:$ROOT/co-from:$FROM"; do
  lbl="${pair%%:*}"; rest="${pair#*:}"; dir="${rest%:*}"; want="${rest##*:}"
  have="$(jq -r .version "$dir/.claude-plugin/plugin.json")"
  if [ "$have" = "$want" ]; then
    check PASS "$lbl source checkout: plugin.json version at tag v$want is $want" "$have"
  else
    check FAIL "$lbl source checkout: plugin.json version at tag v$want is $want" "plugin.json says '$have' at tag v$want"
  fi
  if [ -d "$dir/libexec" ]; then
    check FAIL "$lbl source checkout has no libexec/ (PATH-fallback case)" "libexec/ exists"
  else
    check PASS "$lbl source checkout has no libexec/ (PATH-fallback case)" "no libexec/"
  fi
done

# trivial valid workflow in a git-initialised project
PROJ="$SB_WORK/proj"
mkdir -p "$PROJ/.claude/workflows"
cat >"$PROJ/.claude/workflows/$WF.yaml" <<'YAML'
workflow: trivial
description: >
  Harness-local fixture - one deterministic step, then done.
start: hello

steps:
  - id: hello
    kind: deterministic
    run: 'echo hello'
    next: done

terminal:
  done:    {status: ok,      message: "done"}
  blocked: {status: blocked, message: "blocked: ${blocked_reason}"}
YAML
SB_CWD="$PROJ"
step "git init fixture project" sb git init -q
step "git add" sb git add -A
step "git commit" sb git -c user.name=e2e -c user.email=e2e@example.invalid commit -q -m fixture

# run_case <label> <checkout> <path-bin-dir> : runs the checkout's launcher with only that pawl on PATH
run_case() {
  local label="$1" co="$2" bindir="$3"
  SB_EXTRA_PATH=("$bindir")
  step "$label: which pawl on PATH" sb sh -c 'command -v pawl'
  step "$label: $co/bin/pawl run $WF (PAWL_ENFORCEMENT=off)" sb PAWL_ENFORCEMENT=off "$co/bin/pawl" run "$WF"
  SB_EXTRA_PATH=()
}

# --- F1: newer plugin checkout, OLDER binary (predates the check) ------------
note "F1: plugin checkout $TO (launcher exports PAWL__PLUGIN_VERSION=$TO) + the $FROM binary on PATH. Does $FROM have the version-match check at all? It does not: v$FROM has no internal/cli/version_check.go, and its bin/pawl does not export PAWL__PLUGIN_VERSION. [inferred from the tag trees; the run below is the observation]"
run_case "F1" "$ROOT/co-to" "$ROOT/frombin"
f1_rc="$STEP_RC"
f1_out="$STEP_OUT"
f1_err="$STEP_ERR"
if [ "$f1_rc" -eq 4 ]; then
  check PASS "F1 ($TO plugin + $FROM binary): refused with exit 4" "$f1_err"
elif [ "$f1_rc" -eq 0 ] && printf '%s' "$f1_out" | grep -q '^TERMINAL .* ok'; then
  check PASS "observed F1 ($TO plugin + $FROM binary): NO mismatch refusal, the run simply completes (a binary older than the check cannot enforce it)" "exit $f1_rc; stdout=$(printf '%s' "$f1_out" | grep TERMINAL | head -n1); stderr='$f1_err'"
else
  check FAIL "F1 ($TO plugin + $FROM binary): expected a refusal or a normal run" "exit $f1_rc; stdout=$f1_out; stderr=$f1_err"
fi

# --- F2: older plugin checkout, NEWER binary ----------------------------------
note "F2: plugin checkout $FROM (its bin/pawl predates PAWL__PLUGIN_VERSION, so it exports nothing) + the $TO binary on PATH. The $TO binary's check only fires when the variable is set."
run_case "F2" "$ROOT/co-from" "$ROOT/tobin"
f2_rc="$STEP_RC"
f2_err="$STEP_ERR"
f2_out="$STEP_OUT"
if [ "$f2_rc" -eq 4 ]; then
  check PASS "F2 ($FROM plugin checkout + $TO binary): refused with exit 4" "$f2_err"
elif [ "$f2_rc" -eq 0 ] && printf '%s' "$f2_out" | grep -q '^TERMINAL .* ok'; then
  check PASS "observed F2 ($FROM plugin checkout + $TO binary): NO mismatch refusal, the run completes (the $FROM launcher does not export PAWL__PLUGIN_VERSION)" "exit $f2_rc; stdout=$(printf '%s' "$f2_out" | grep TERMINAL | head -n1); stderr='$f2_err'"
else
  check FAIL "F2 ($FROM plugin checkout + $TO binary): expected a refusal or a normal run" "exit $f2_rc; stdout=$f2_out; stderr=$f2_err"
fi

# --- F3/F4: synthetic - exercise the check TO actually has -------------------
# A copy of the TO checkout with plugin.json edited; everything else untouched.
mk_variant() { # <ver> <dest>
  cp -R "$ROOT/co-to" "$2"
  sed -i 's/"version"[[:space:]]*:[[:space:]]*"[^"]*"/"version": "'"$1"'"/' "$2/.claude-plugin/plugin.json"
}
check_refusal() { # <label> <plugin-ver> <bin-ver>
  local label="$1" pv="$2" bv="$3"
  if [ "$STEP_RC" -eq 4 ]; then
    check PASS "$label: exit code is 4" "stderr: $STEP_ERR"
  else
    check FAIL "$label: exit code is 4" "exit $STEP_RC stdout=$STEP_OUT stderr=$STEP_ERR"
    return
  fi
  if printf '%s' "$STEP_ERR" | grep -q "$pv" && printf '%s' "$STEP_ERR" | grep -q "$bv"; then
    check PASS "$label: stderr names both versions ($bv binary, $pv plugin)" "$STEP_ERR"
  else
    check FAIL "$label: stderr names both versions ($bv binary, $pv plugin)" "$STEP_ERR"
  fi
  if printf '%s' "$STEP_ERR" | grep -Eq '/plugin |install pawl|go install|install\.sh'; then
    check PASS "$label: stderr states a concrete fix" "$(printf '%s' "$STEP_ERR" | grep -Eo 'Reinstall.*' | head -n1)"
  else
    check FAIL "$label: stderr states a concrete fix" "$STEP_ERR"
  fi
}

mk_variant "$FROM" "$ROOT/co-to-as-from"
note "F3: SYNTHETIC. TO checkout with plugin.json edited to $FROM (an OLDER plugin) + the $TO binary on PATH: binary newer than plugin. (The real $FROM launcher cannot produce this state: see F2.)"
run_case "F3" "$ROOT/co-to-as-from" "$ROOT/tobin"
check_refusal "F3 (plugin $FROM, binary $TO)" "$FROM" "$TO"

mk_variant "0.9.0" "$ROOT/co-to-as-next"
note "F4: SYNTHETIC. TO checkout with plugin.json edited to 0.9.0 (a NEWER plugin, as after a Claude Code plugin update) + the $TO binary on PATH: binary older than plugin, same check."
run_case "F4" "$ROOT/co-to-as-next" "$ROOT/tobin"
check_refusal "F4 (plugin 0.9.0, binary $TO)" "0.9.0" "$TO"

# --- is the stated fix right for a --plugin-dir / source-checkout user? ------
note "FIX FIT [inferred]: the refusal says: \"Reinstall the plugin (/plugin marketplace update agent-pawl, then reinstall it), or install pawl <plugin version> on PATH.\" For a --plugin-dir / source-checkout user the first half does not apply (that dir is not marketplace-managed; reinstalling the marketplace plugin does not change what --plugin-dir points at), the second half applies but names no command (install.sh with PAWL_VERSION=v<ver>, or go install github.com/dcferreira/agent-pawl/cmd/pawl@v<ver>, are the actual means)."
step "docs: does the repo document PAWL_VERSION for install.sh? (grep at tag v$TO)" grep -n "PAWL_VERSION" "$ROOT/co-to/install.sh" "$ROOT/co-to/docs/install.md"

# --- skill reaction -------------------------------------------------------------
step "SKILL.md at v$TO: enforcement/refusal paragraph (lines 19-26)" sed -n 19,26p "$ROOT/co-to/skills/pawl/SKILL.md"
step "SKILL.md at v$TO: exit-code paragraph (lines 55-62)" sed -n 55,62p "$ROOT/co-to/skills/pawl/SKILL.md"
step "SKILL.md at v$TO: any mention of version mismatch / exit 4 / plugin version" grep -n -i "mismatch\|PAWL__PLUGIN_VERSION\|plugin expects\|exit 4" "$ROOT/co-to/skills/pawl/SKILL.md"
note "[inferred] SKILL.md at v$TO does not mention the version-mismatch refusal. Its exit-code paragraph says exit 4 means 'it refused the request ... and printed why on stderr', and to 'stop to tell the user' only on exit 1, 2 or 5, or on exit 3/4 'whose stderr you can't otherwise explain'. So an agent may reasonably read the mismatch message as explained and act on its 'Reinstall the plugin ... or install pawl X on PATH' advice itself (running installs) rather than stopping to report. Whether it does can only be observed in a live agent session."
check SKIP "skill stops instead of looping (needs live agent session)" "see the [inferred] note in the transcript; not observable without a model-driven session"

e2e_finish
