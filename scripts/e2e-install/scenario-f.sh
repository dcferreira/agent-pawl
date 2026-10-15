#!/usr/bin/env bash
# Scenario F: plugin/binary version-mismatch UX. A source checkout used as a
# plugin dir (no libexec/) falls back to a `pawl` on PATH; this scenario
# records what happens when that PATH binary is the wrong version.
#   F1 (realistic): TO checkout (plugin.json TO) + the FROM release binary on PATH.
#   F2 (realistic): FROM checkout (plugin.json FROM) + the TO release binary on PATH.
#   F3/F4 (synthetic): TO checkout whose plugin.json is edited to a different
#       version, with the TO binary on PATH, to exercise the check that exists
#       in TO.
#   F5a/b/c: the heartbeat-recorded plugin version (PR #75) and PAWL__LAUNCHER;
#       each gated on PAWL_FIRST_HEARTBEAT_CHECK (lib.sh), SKIP before it.
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
  sed -i.bak 's/"version"[[:space:]]*:[[:space:]]*"[^"]*"/"version": "'"$1"'"/' "$2/.claude-plugin/plugin.json" && rm -f "$2/.claude-plugin/plugin.json.bak"
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

# --- F5: heartbeat-recorded plugin version (PR #75) ---------------------------
# A stale `pawl` earlier on PATH shadows the plugin's binary (Claude Code appends
# a plugin's bin/ to the END of PATH). The fix: `pawl hook pre`, always run through
# the plugin's own bin/pawl-hook + launcher, records plugin_version in the
# heartbeat; `pawl run` refuses (exit 4) when that differs from its own version,
# before it loads or validates the workflow; and the launcher exports
# PAWL__LAUNCHER, which pawl prints as the command in `submit with:` lines.
#
# PRE-RELEASE TESTING HOOKS (documented in README.md; unset in normal use):
#   E2E_TO_PLUGIN_DIR=<dir>   use this plugin dir (bin/ hooks/ .claude-plugin/
#                             libexec/<plat>/pawl) as "the installed TO plugin"
#                             instead of installing TO from the marketplace; the
#                             F5a/F5c gate then uses that dir's plugin.json version.
#   E2E_FROM_BINARY=<file>    use this pawl binary as "the FROM release binary"
#                             for F5b (gate = the version it reports).
f5_wf_setup() {
  cat >"$PROJ/.claude/workflows/f5-agentic.yaml" <<'YAML'
workflow: f5-agentic
description: >
  Harness-local fixture - one agentic step, then done.
start: work
state: {note: {type: string}}

steps:
  - id: work
    kind: agentic
    description: |
      Harness fixture; nobody submits this step.
    writes: {note: {type: string}}
    postcondition: {command: "true"}
    next: done

terminal:
  done:    {status: ok,      message: "done"}
  blocked: {status: blocked, message: "blocked"}
YAML
  cat >"$PROJ/.claude/workflows/f5-blocked.yaml" <<'YAML'
workflow: f5-blocked
description: >
  Harness-local fixture - routes straight to a blocked terminal.
start: hello

steps:
  - id: hello
    kind: deterministic
    run: 'echo hello'
    next: blocked

terminal:
  done:    {status: ok,      message: "done"}
  blocked: {status: blocked, message: "blocked on purpose"}
YAML
  cat >"$PROJ/.claude/workflows/f5-invalid.yaml" <<'YAML'
workflow: f5-invalid
description: >
  Harness-local fixture - deliberately invalid (unknown top-level field).
start: hello
this_field_does_not_exist: true

steps:
  - id: hello
    kind: deterministic
    run: 'echo hello'
    next: done

terminal:
  done:    {status: ok,      message: "done"}
  blocked: {status: blocked, message: "blocked"}
YAML
}

# f5_install_to_plugin: sets F5_PLUGIN (installed TO plugin dir) and F5_TO_VER.
f5_install_to_plugin() {
  F5_PLUGIN=""
  F5_TO_VER="$TO"
  if [ -n "${E2E_TO_PLUGIN_DIR:-}" ]; then
    [ -f "$E2E_TO_PLUGIN_DIR/.claude-plugin/plugin.json" ] || { check FAIL "E2E_TO_PLUGIN_DIR is a plugin dir" "$E2E_TO_PLUGIN_DIR"; return 1; }
    F5_PLUGIN="$ROOT/prerelease-plugin"
    cp -R "$E2E_TO_PLUGIN_DIR" "$F5_PLUGIN"
    F5_TO_VER="$(jq -r .version "$F5_PLUGIN/.claude-plugin/plugin.json")"
    note "F5: PRE-RELEASE HOOK: using E2E_TO_PLUGIN_DIR=$E2E_TO_PLUGIN_DIR (copied to $F5_PLUGIN, version $F5_TO_VER) instead of a marketplace install of $TO."
    return 0
  fi
  install_plugin_version "$TO" || return 1
  F5_PLUGIN="$PLUGIN_DIR"
}

# f5_write_heartbeat <plugin-dir> <session-id>: run that plugin's bin/pawl-hook pre
# with a synthetic PreToolUse payload for a pawl command, cwd = the fixture project.
f5_write_heartbeat() {
  local payload="$ROOT/f5-payload-$2.json"
  jq -n --arg s "$2" --arg c "$PROJ" \
    '{session_id: $s, cwd: $c, hook_event_name: "PreToolUse", tool_name: "Bash", tool_input: {command: "pawl run trivial"}}' >"$payload"
  # shellcheck disable=SC2016 # $1/$2 expand in the inner sh, not here
  step "F5: $1/bin/pawl-hook pre (session $2) writes the heartbeat" \
    sb sh -c '"$1/bin/pawl-hook" pre <"$2"' f5 "$1" "$payload"
  note "heartbeat file: $(cat "$SB_HOME/.claude/pawl/heartbeat/session/$2.json" 2>/dev/null || echo '(none)')"
}

f5_check_refusal() { # <label> <bin-ver> <plugin-ver>
  if [ "$STEP_RC" -eq 4 ]; then
    check PASS "$1: exit code is 4" "stderr: $STEP_ERR"
  else
    check FAIL "$1: exit code is 4" "exit $STEP_RC stdout=$STEP_OUT stderr=$STEP_ERR"
    return
  fi
  if printf '%s' "$STEP_ERR" | grep -q "refusing to start: this pawl binary" &&
    printf '%s' "$STEP_ERR" | grep -q "is $2 but the agent-pawl plugin expects $3"; then
    check PASS "$1: version refusal names binary $2 and plugin $3" "$STEP_ERR"
  else
    check FAIL "$1: version refusal names binary $2 and plugin $3" "$STEP_ERR"
  fi
}

f5_gate_note() {
  note "F5 gate: $1 needs PR #75's heartbeat check, first shipped in $PAWL_FIRST_HEARTBEAT_CHECK (PAWL_FIRST_HEARTBEAT_CHECK in lib.sh; correct it there if that fix shipped in a different release). $2"
}

SB_CWD="$PROJ"
f5_wf_setup
F5_PLUGIN=""
F5_TO_VER="$TO"
F5_READY=0
if [ -n "${E2E_TO_PLUGIN_DIR:-}" ] || ver_ge "$TO" "$PAWL_FIRST_HEARTBEAT_CHECK"; then
  if f5_install_to_plugin; then F5_READY=1; else check FAIL "F5: TO plugin installed" "see transcript"; fi
fi
F5_TO_BIN="$F5_PLUGIN/libexec/$(plat)/pawl"

# F5a (synthetic): the hook's launcher says 99.0.0; the binary that runs `pawl run`
# is the real TO binary invoked directly (no launcher, PAWL__PLUGIN_VERSION unset),
# i.e. what a shadowing `pawl` looks like. Enforcement stays ON so the heartbeat is used.
f5_gate_note "F5a (TO=$F5_TO_VER)" "F5a: SYNTHETIC. The installed plugin is copied with plugin.json edited to 99.0.0; its bin/pawl-hook writes the heartbeat; the real TO libexec binary then runs \`pawl run\` directly with CLAUDE_CODE_SESSION_ID set."
if ver_ge "$F5_TO_VER" "$PAWL_FIRST_HEARTBEAT_CHECK"; then
  if [ "$F5_READY" -eq 1 ] && [ -x "$F5_TO_BIN" ]; then
    cp -R "$F5_PLUGIN" "$ROOT/plugin-as-99"
    sed -i.bak 's/"version"[[:space:]]*:[[:space:]]*"[^"]*"/"version": "99.0.0"/' "$ROOT/plugin-as-99/.claude-plugin/plugin.json" && rm -f "$ROOT/plugin-as-99/.claude-plugin/plugin.json.bak"
    f5_write_heartbeat "$ROOT/plugin-as-99" e2e-f5-session
    hbv="$(jq -r '.plugin_version // empty' "$SB_HOME/.claude/pawl/heartbeat/session/e2e-f5-session.json" 2>/dev/null)"
    if [ "$hbv" = "99.0.0" ]; then
      check PASS "F5a: hook heartbeat records plugin_version 99.0.0" "$hbv"
    else
      check FAIL "F5a: hook heartbeat records plugin_version 99.0.0" "plugin_version='$hbv'"
    fi
    f5_bv="$(version_of "$F5_TO_BIN")"
    step "F5a: real TO binary: pawl run $WF (session e2e-f5-session, enforcement ON, no PAWL__PLUGIN_VERSION)" \
      sb CLAUDE_CODE_SESSION_ID=e2e-f5-session "$F5_TO_BIN" run "$WF"
    f5_check_refusal "F5a (valid workflow; binary $f5_bv, heartbeat plugin 99.0.0)" "$f5_bv" "99.0.0"
    step "F5a: real TO binary: pawl run f5-invalid (same session; check must precede validation)" \
      sb CLAUDE_CODE_SESSION_ID=e2e-f5-session "$F5_TO_BIN" run f5-invalid
    f5_check_refusal "F5a (invalid workflow; version check runs before validation)" "$f5_bv" "99.0.0"
  else
    check FAIL "F5a: TO plugin and its libexec binary available" "plugin='$F5_PLUGIN' bin='$F5_TO_BIN'"
  fi
else
  check SKIP "F5a: heartbeat-recorded plugin version refusal (synthetic 99.0.0 plugin)" "TO=$F5_TO_VER < $PAWL_FIRST_HEARTBEAT_CHECK (PAWL_FIRST_HEARTBEAT_CHECK): TO has no heartbeat check to exercise"
fi

# F5b (real): the FROM binary shadows the TO plugin's on PATH, as Claude Code would
# have it (plugin bin/ last). The heartbeat is written by the TO plugin's own hook.
f5_from_ver="$FROM"
[ -n "${E2E_FROM_BINARY:-}" ] && f5_from_ver="$(version_of "$E2E_FROM_BINARY")"
f5_gate_note "F5b (FROM=$f5_from_ver)" "F5b: REAL. The FROM release binary must itself contain the check to refuse; a FROM older than that gate cannot, which is why this skips."
if ver_ge "$f5_from_ver" "$PAWL_FIRST_HEARTBEAT_CHECK"; then
  if [ "$F5_READY" -eq 1 ] && [ "$f5_from_ver" != "$F5_TO_VER" ]; then
    mkdir -p "$ROOT/f5-shadow"
    if [ -n "${E2E_FROM_BINARY:-}" ]; then
      cp "$E2E_FROM_BINARY" "$ROOT/f5-shadow/pawl" && chmod +x "$ROOT/f5-shadow/pawl"
      note "F5b: PRE-RELEASE HOOK: shadow binary is E2E_FROM_BINARY=$E2E_FROM_BINARY"
    else
      fetch_release_binary "$FROM" "$ROOT/f5-shadow/pawl" || check FAIL "F5b: fetch $FROM release binary" "see transcript"
    fi
    if [ -x "$ROOT/f5-shadow/pawl" ]; then
      f5_write_heartbeat "$F5_PLUGIN" e2e-f5b-session
      SB_EXTRA_PATH=("$ROOT/f5-shadow" "$F5_PLUGIN/bin")
      step "F5b: which pawl on PATH (shadow first, plugin bin last)" sb sh -c 'command -v pawl'
      step "F5b: pawl run $WF via PATH (session e2e-f5b-session, enforcement ON)" sb CLAUDE_CODE_SESSION_ID=e2e-f5b-session pawl run "$WF"
      SB_EXTRA_PATH=()
      f5_check_refusal "F5b ($f5_from_ver binary shadowing the $F5_TO_VER plugin)" "$f5_from_ver" "$F5_TO_VER"
    fi
  else
    check SKIP "F5b: shadowing FROM binary vs TO plugin" "F5b needs a TO plugin installed and FROM ($f5_from_ver) different from TO ($F5_TO_VER)"
  fi
else
  check SKIP "F5b: FROM release binary shadowing the TO plugin on PATH" "FROM=$f5_from_ver < $PAWL_FIRST_HEARTBEAT_CHECK (PAWL_FIRST_HEARTBEAT_CHECK): the FROM binary predates the check and cannot refuse"
fi

# F5c: instruction lines through the installed TO plugin's own launcher. Enforcement
# is OFF here (PAWL_ENFORCEMENT=off): only the printed command names matter, and no
# hook heartbeat is needed for that.
f5_gate_note "F5c (TO=$F5_TO_VER)" "F5c: through the TO plugin's bin/pawl launcher (enforcement OFF: only the printed command names are examined)."
if ver_ge "$F5_TO_VER" "$PAWL_FIRST_HEARTBEAT_CHECK"; then
  if [ "$F5_READY" -eq 1 ]; then
    f5_launcher="$(cd "$F5_PLUGIN/bin" && pwd -P)/pawl"
    step "F5c: $f5_launcher run f5-agentic" sb PAWL_ENFORCEMENT=off "$f5_launcher" run f5-agentic
    f5_submit="$(printf '%s\n' "$STEP_OUT" | grep -m1 'submit with:' || true)"
    case "$(printf '%s' "$f5_submit" | sed 's/^[[:space:]]*submit with:[[:space:]]*//')" in
      "'$f5_launcher' submit "*) check PASS "F5c: submit with: line begins with the shell-quoted launcher path" "$f5_submit" ;;
      *) check FAIL "F5c: submit with: line begins with the shell-quoted launcher path" "expected '$f5_launcher' submit ...; got: ${f5_submit:-(no submit with: line) exit $STEP_RC stdout=$STEP_OUT stderr=$STEP_ERR}" ;;
    esac
    step "F5c: $f5_launcher run f5-blocked (blocked TERMINAL)" sb PAWL_ENFORCEMENT=off "$f5_launcher" run f5-blocked
    # the TERMINAL prints "resume:" / "abandon:" alone on a line, the command on the next
    f5_res="$(printf '%s\n' "$STEP_OUT" | grep -E -A1 '^[[:space:]]*resume:' | tr '\n' ' ' || true)"
    f5_aba="$(printf '%s\n' "$STEP_OUT" | grep -E -A1 '^[[:space:]]*abandon:' | tr '\n' ' ' || true)"
    if [ -z "$f5_res" ] || [ -z "$f5_aba" ]; then
      check SKIP "F5c: TERMINAL resume:/abandon: lines use plain pawl" "no resume:/abandon: line in the blocked TERMINAL (exit $STEP_RC): $STEP_OUT"
    elif printf '%s\n%s\n' "$f5_res" "$f5_aba" | grep -qF "$f5_launcher"; then
      check FAIL "F5c: TERMINAL resume:/abandon: lines use plain pawl" "launcher path leaked: $f5_res | $f5_aba"
    elif printf '%s' "$f5_res" | grep -Eq 'resume:[[:space:]]+pawl ' && printf '%s' "$f5_aba" | grep -Eq 'abandon:[[:space:]]+pawl '; then
      check PASS "F5c: TERMINAL resume:/abandon: lines use plain pawl" "$f5_res | $f5_aba"
    else
      check FAIL "F5c: TERMINAL resume:/abandon: lines use plain pawl" "$f5_res | $f5_aba"
    fi
  else
    check FAIL "F5c: TO plugin available" "see transcript"
  fi
else
  check SKIP "F5c: launcher path in submit with: lines" "TO=$F5_TO_VER < $PAWL_FIRST_HEARTBEAT_CHECK (PAWL_FIRST_HEARTBEAT_CHECK): TO's pawl does not print PAWL__LAUNCHER"
fi

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
