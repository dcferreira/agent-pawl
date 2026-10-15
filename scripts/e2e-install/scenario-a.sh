#!/usr/bin/env bash
# Scenario A: fresh Claude Code marketplace install of the plugin (live
# marketplace, or a directory marketplace in SOURCE=local), then drive the
# green-tests example to TERMINAL ok through the installed bin/pawl launcher.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
# shellcheck source-path=SCRIPTDIR
. "$HERE/lib.sh"

e2e_init a
P="$(plat)"

install_plugin || { check FAIL "plugin installed" "see transcript"; e2e_finish; exit $?; }

# --- the installed tree ---
bin_real="$PLUGIN_DIR/libexec/$P/pawl"
if [ -x "$bin_real" ]; then
  check PASS "libexec binary for $P exists" "$bin_real"
else
  check FAIL "libexec binary for $P exists" "missing: $bin_real"
fi

plugin_ver="$(jq -r .version "$PLUGIN_DIR/.claude-plugin/plugin.json")"
step "bin/pawl version (installed launcher)" sb "$PLUGIN_DIR/bin/pawl" version
launcher_ver="$(printf '%s\n' "$STEP_OUT" | awk 'NR==1 && $1=="pawl" {print $2}')"
if [ "$plugin_ver" = "$TO" ] && [ "$launcher_ver" = "$plugin_ver" ]; then
  check PASS "bin/pawl version == plugin.json version == TO" "$launcher_ver"
else
  check FAIL "bin/pawl version == plugin.json version == TO" "launcher=$launcher_ver plugin.json=$plugin_ver TO=$TO"
fi

# --- pawl on the sandbox PATH is the plugin launcher (symlink, as a plugin's bin/ on PATH) ---
ln -s "$PLUGIN_DIR/bin/pawl" "$SB_BIN/pawl"
assert_pawl_in_sandbox
step "pawl version via sandbox PATH symlink" sb pawl version
if printf '%s\n' "$STEP_OUT" | grep -q "^pawl $TO\$"; then
  check PASS "symlinked launcher on PATH resolves the bundled binary" "$STEP_OUT"
else
  check FAIL "symlinked launcher on PATH resolves the bundled binary" "$STEP_OUT"
fi

# --- prove the launcher exports PAWL__PLUGIN_VERSION (black-box) ---
# Honest approach: copy the installed plugin into the sandbox, swap ONLY the
# copy's libexec binary for a stub that prints the env var, and run the copy's
# real launcher. Then, with the real binary, edit the copy's plugin.json to a
# different version and confirm `pawl run` refuses (the engine consumes it).
probe="$ROOT/probe-plugin"
cp -R "$PLUGIN_DIR" "$probe"
# shellcheck disable=SC2016 # the stub script must receive the literal ${...}, expanded by sh later
printf '#!/bin/sh\necho "v=[${PAWL__PLUGIN_VERSION-UNSET}]"\n' >"$probe/libexec/$P/pawl"
chmod +x "$probe/libexec/$P/pawl"
step "launcher with stub libexec binary (env dump)" sb "$probe/bin/pawl"
if [ "$STEP_OUT" = "v=[$TO]" ]; then
  check PASS "launcher exports PAWL__PLUGIN_VERSION=plugin.json version" "$STEP_OUT"
else
  check FAIL "launcher exports PAWL__PLUGIN_VERSION=plugin.json version" "$STEP_OUT"
fi

probe2="$ROOT/probe-plugin-mismatch"
cp -R "$PLUGIN_DIR" "$probe2"
sed -i.bak 's/"version"[[:space:]]*:[[:space:]]*"[^"]*"/"version": "99.0.0"/' "$probe2/.claude-plugin/plugin.json" && rm -f "$probe2/.claude-plugin/plugin.json.bak"
mkdir -p "$ROOT/work/mismatch"
SB_CWD="$ROOT/work/mismatch" step "pawl run through launcher whose plugin.json says 99.0.0 (expect version-mismatch refusal)" \
  sb PAWL_ENFORCEMENT=off "$probe2/bin/pawl" run green-tests
if [ "$STEP_RC" -ne 0 ] && printf '%s' "$STEP_ERR" | grep -q "plugin expects 99.0.0"; then
  check PASS "real binary receives PAWL__PLUGIN_VERSION (mismatch refused)" "$(printf '%s' "$STEP_ERR" | head -n1)"
else
  check FAIL "real binary receives PAWL__PLUGIN_VERSION (mismatch refused)" "rc=$STEP_RC stderr=$STEP_ERR"
fi

# --- green-tests through the installed bin/pawl, no model ---
src="$ROOT/src"
fetch_source_tree "$TO" "$src" || { check FAIL "fetch v$TO source tree" "see transcript"; e2e_finish; exit $?; }
gt="$SB_WORK/green"
mkdir -p "$gt/.claude/workflows/scripts"
cp "$src"/testdata/fixture/go.mod "$src"/testdata/fixture/fixture.go "$src"/testdata/fixture/fixture_test.go "$gt/"
cp "$src/docs/examples/green-tests/workflow.yaml" "$gt/.claude/workflows/green-tests.yaml"
cp "$src/docs/examples/green-tests/scripts/run-tests.sh" "$gt/.claude/workflows/scripts/run-tests.sh"
chmod +x "$gt/.claude/workflows/scripts/run-tests.sh"
SB_CWD="$gt"
step "git init fixture project" sb git init -q
step "git add" sb git add -A
step "git commit" sb git -c user.name=e2e -c user.email=e2e@example.invalid commit -q -m fixture

note "NOTE: the following pawl calls run with PAWL_ENFORCEMENT=off (no hooks session exists in this harness); the agentic fix_tests step is satisfied by the harness itself editing fixture.go (as e2e/green_tests_test.go does), then submitting."
step "pawl run green-tests (via installed bin/pawl)" sb PAWL_ENFORCEMENT=off "$PLUGIN_DIR/bin/pawl" run green-tests
run_out="$STEP_OUT"
run_id="$(printf '%s\n' "$run_out" | awk '$1=="DISPATCH" {print $2; exit}')"
if [ "$STEP_RC" -eq 0 ] && [ -n "$run_id" ] && printf '%s' "$run_out" | grep -q 'fix_tests'; then
  check PASS "green-tests reaches DISPATCH of fix_tests" "run $run_id"
else
  check FAIL "green-tests reaches DISPATCH of fix_tests" "rc=$STEP_RC out=$run_out err=$STEP_ERR"
  e2e_finish
  exit $?
fi

step "harness applies the fix (Add: a - b -> a + b)" sb sed -i.bak 's/return a - b/return a + b/' "$gt/fixture.go"
rm -f "$gt/fixture.go.bak"
step "pawl submit fix_tests (via installed bin/pawl)" sb PAWL_ENFORCEMENT=off "$PLUGIN_DIR/bin/pawl" submit \
  --run "$run_id" --step fix_tests --json '{"fix_summary":"harness fixed Add"}'
if [ "$STEP_RC" -eq 0 ] && printf '%s' "$STEP_OUT" | grep -q "TERMINAL $run_id ok"; then
  check PASS "green-tests reaches TERMINAL ok through installed bin/pawl" "$(printf '%s' "$STEP_OUT" | grep "TERMINAL" | head -n1)"
else
  check FAIL "green-tests reaches TERMINAL ok through installed bin/pawl" "rc=$STEP_RC out=$STEP_OUT err=$STEP_ERR"
fi

e2e_finish
