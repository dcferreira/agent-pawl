#!/usr/bin/env bash
# Scenario E: mid-run upgrade. A run is started through the FROM plugin and is
# blocked on a human step; the plugin is updated to TO underneath it; the FROM
# plugin dir is then deleted (a session whose PATH/CLAUDE_PLUGIN_ROOT still
# points at it); the run is finished through the TO launcher.
# Everything the stale-session commands print is recorded verbatim.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
# shellcheck source-path=SCRIPTDIR
. "$HERE/lib.sh"

e2e_init e
P="$(plat)"
WF=upgrade-fixture

# hook_call <hook-path> <pre|stop> <payload-json>: feed the payload on stdin.
hook_call() {
  # shellcheck disable=SC2016 # the sh -c script is single-quoted on purpose; it expands inside the sandbox shell
  sb sh -c 'printf "%s" "$1" | "$2" "$3"' hook_call "$3" "$1" "$2"
}
# rc_ok <rc>: a hook must never exit 2 (Claude Code reads 2 as "block").
not_block() { [ "$1" -ne 2 ]; }

# --- 1. FROM plugin installed ------------------------------------------------
install_plugin_version "$FROM" || { check FAIL "FROM plugin installed" "see transcript"; e2e_finish; exit $?; }
check PASS "method used to install $FROM" "$INSTALL_METHOD"
OLD_DIR="$PLUGIN_DIR"
OLD_LAUNCHER="$OLD_DIR/bin/pawl"
OLD_HOOK="$OLD_DIR/bin/pawl-hook"
note "OLD_LAUNCHER=$OLD_LAUNCHER OLD_HOOK=$OLD_HOOK"
# A session's PATH has the plugin's bin/ (or a symlink to its launcher) on it.
ln -s "$OLD_LAUNCHER" "$SB_BIN/pawl"
got="$(version_of "$OLD_DIR/libexec/$P/pawl")"
if [ "$got" = "$FROM" ]; then
  check PASS "FROM libexec binary reports $FROM" "$got"
else
  check FAIL "FROM libexec binary reports $FROM" "got '$got'"
fi

# --- 2. fixture workflow, valid on both versions ------------------------------
PROJ="$SB_WORK/proj"
mkdir -p "$PROJ/.claude/workflows"
cat >"$PROJ/.claude/workflows/$WF.yaml" <<'YAML'
workflow: upgrade-fixture
description: >
  Harness-local fixture for the install/update scenarios: block on a human
  question, then record the answer deterministically. Valid on 0.7.0 and 0.8.0.
start: ask

state:
  decision: {type: string, default: ""}

steps:
  - id: ask
    kind: human
    question: |
      Proceed with the upgrade fixture?
    options: [go, stop]
    timeout: 1h
    writes: [decision]
    outcomes:
      go: record
      stop: blocked
      timeout: blocked

  - id: record
    kind: deterministic
    run: 'echo recorded: ${decision}'
    next: done

terminal:
  done:    {status: ok,      message: "Recorded: ${decision}"}
  blocked: {status: blocked, message: "Stopped or timed out: ${blocked_reason}"}
YAML
SB_CWD="$PROJ"
step "git init fixture project" sb git init -q
step "git add" sb git add -A
step "git commit" sb git -c user.name=e2e -c user.email=e2e@example.invalid commit -q -m fixture
step "validate fixture with the $FROM binary" sb "$OLD_DIR/libexec/$P/pawl" validate "$WF"
if [ "$STEP_RC" -eq 0 ]; then
  check PASS "fixture workflow validates on $FROM" "$STEP_OUT"
else
  check FAIL "fixture workflow validates on $FROM" "rc=$STEP_RC $STEP_OUT $STEP_ERR"
fi

# --- 3. start the run through the FROM plugin's launcher ----------------------
note "NOTE: pawl run is started with PAWL_ENFORCEMENT=off (no Claude Code session exists in this harness, so no PreToolUse heartbeat can be on disk). Hooks are exercised separately below with synthetic payloads."
step "pawl run $WF via OLD_LAUNCHER ($FROM)" sb PAWL_ENFORCEMENT=off "$OLD_LAUNCHER" run "$WF"
run_out="$STEP_OUT"
RUN_ID="$(printf '%s\n' "$run_out" | awk '$1=="ASK" {print $2; exit}')"
if [ "$STEP_RC" -eq 0 ] && [ -n "$RUN_ID" ] && printf '%s' "$run_out" | grep -q '^ASK '; then
  check PASS "run blocks on the human step (ASK) under $FROM" "run $RUN_ID"
else
  check FAIL "run blocks on the human step (ASK) under $FROM" "rc=$STEP_RC out=$run_out err=$STEP_ERR"
  e2e_finish
  exit $?
fi
RUN_DIR="$(find "$SB_HOME/.claude/pawl/runs" -type d -name "$RUN_ID" 2>/dev/null | head -n1)"
note "run dir: $RUN_DIR"
EV="$RUN_DIR/events.jsonl"
step "run dir listing" ls -la "$RUN_DIR"
step "events.jsonl under $FROM" cat "$EV"
first_fmt="$(head -n1 "$EV" | jq -r '.format // "absent"')"
check PASS "observed: RUN_START format of the run started by $FROM" "$first_fmt (absent = unstamped, FROM < 0.8.0)"
[ -f "$RUN_DIR/plan.json" ] && note "plan.json \"format\" key: $(jq -r '.format // "absent"' "$RUN_DIR/plan.json")"

# --- 4. update to TO -----------------------------------------------------------
update_plugin_to "$TO" || { check FAIL "update $FROM -> $TO" "see transcript"; e2e_finish; exit $?; }
NEW_DIR="$PLUGIN_DIR"
NEW_LAUNCHER="$NEW_DIR/bin/pawl"
NEW_HOOK="$NEW_DIR/bin/pawl-hook"
if [ -d "$OLD_DIR" ]; then
  check PASS "observed: old $FROM cache dir after update: kept" "claude $(sb claude --version | head -n1)"
else
  check PASS "observed: old $FROM cache dir after update: deleted" "claude $(sb claude --version | head -n1)"
fi

# --- 5. hooks during the swap (both plugin dirs present) -----------------------
SESS="e2e-session-0001"
PRE="{\"session_id\":\"$SESS\",\"cwd\":\"$PROJ\",\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"pawl status\"}}"
STOP="{\"session_id\":\"$SESS\",\"cwd\":\"$PROJ\",\"hook_event_name\":\"Stop\",\"stop_hook_active\":false,\"background_tasks\":[],\"session_crons\":[]}"
for pair in "OLD_HOOK:$OLD_HOOK" "NEW_HOOK:$NEW_HOOK"; do
  label="${pair%%:*}"
  hook="${pair#*:}"
  for ev in pre stop; do
    payload="$PRE"
    [ "$ev" = stop ] && payload="$STOP"
    step "$label $ev (both plugin dirs present, live run at the human step)" hook_call "$hook" "$ev" "$payload"
    if not_block "$STEP_RC"; then
      check PASS "$label $ev during the swap: not a block (exit $STEP_RC)" "stdout='$STEP_OUT' stderr='$STEP_ERR'"
    else
      check FAIL "$label $ev during the swap: not a block (exit $STEP_RC)" "stdout='$STEP_OUT' stderr='$STEP_ERR'"
    fi
  done
done

# --- 6. the old plugin dir vanishes --------------------------------------------
real_old="$(readlink -f "$OLD_DIR")"
case "$real_old" in
  "$ROOT"/claude-config/plugins/cache/*/agent-pawl/"$FROM") ;;
  *) die "refusing to delete '$real_old': not the sandbox's own $FROM plugin cache dir" ;;
esac
if [ -d "$real_old" ]; then
  note "The update kept the $FROM dir, so the harness deletes exactly that dir ($real_old) to simulate a session whose PATH / CLAUDE_PLUGIN_ROOT still points at a plugin dir that has been garbage-collected."
  rm -rf "${real_old:?}"
else
  note "The $FROM dir was already gone after the update."
fi
if [ ! -e "$OLD_DIR" ]; then
  check PASS "old $FROM dir is gone" "$OLD_DIR"
else
  check FAIL "old $FROM dir is gone" "$OLD_DIR still exists"
fi
if [ -d "$NEW_DIR" ]; then
  check PASS "new $TO dir still present" "$NEW_DIR"
else
  check FAIL "new $TO dir still present" "$NEW_DIR is missing"
fi

note "WORDING CAVEAT: the harness runs every command via 'env -i ... <cmd>', so a missing launcher is reported by env(1) as \"env: '<path>': No such file or directory\"; a user's shell would say \"<path>: No such file or directory\" (bash) or \"no such file or directory: <path>\" (zsh). The exit code (127) is the same, and the stale hook's message below comes from sh itself."
step "STALE: OLD_LAUNCHER version" sb "$OLD_LAUNCHER" version
note "OBSERVED: OLD_LAUNCHER version -> exit $STEP_RC; stdout '$STEP_OUT'; stderr '$STEP_ERR'"
check PASS "observed: OLD_LAUNCHER version after its dir was deleted" "exit $STEP_RC; stdout='$STEP_OUT'; stderr='$STEP_ERR'"
step "STALE: pawl version via PATH symlink (dangling)" sb pawl version
check PASS "observed: PATH 'pawl' (symlink to the deleted launcher) version" "exit $STEP_RC; stdout='$STEP_OUT'; stderr='$STEP_ERR'"
step "STALE: OLD_LAUNCHER status" sb "$OLD_LAUNCHER" status
check PASS "observed: OLD_LAUNCHER status after its dir was deleted" "exit $STEP_RC; stdout='$STEP_OUT'; stderr='$STEP_ERR'"
step "STALE: OLD_LAUNCHER status --run $RUN_ID" sb "$OLD_LAUNCHER" status --run "$RUN_ID"
step "STALE: OLD_LAUNCHER submit (answer the human step)" sb "$OLD_LAUNCHER" submit --run "$RUN_ID" --step ask --json '{"selected":["go"]}'
check PASS "observed: OLD_LAUNCHER submit after its dir was deleted" "exit $STEP_RC; stdout='$STEP_OUT'; stderr='$STEP_ERR'"
step "STALE: OLD_LAUNCHER run $WF (resume)" sb PAWL_ENFORCEMENT=off "$OLD_LAUNCHER" run "$WF"
check PASS "observed: OLD_LAUNCHER run (resume) after its dir was deleted" "exit $STEP_RC; stdout='$STEP_OUT'; stderr='$STEP_ERR'"
for ev in pre stop; do
  payload="$PRE"
  [ "$ev" = stop ] && payload="$STOP"
  step "STALE: OLD_HOOK $ev (dir deleted)" hook_call "$OLD_HOOK" "$ev" "$payload"
  if not_block "$STEP_RC"; then
    check PASS "OLD_HOOK $ev after its dir was deleted: not exit 2 (non-blocking; exit $STEP_RC)" "stdout='$STEP_OUT'; stderr='$STEP_ERR'"
  else
    check FAIL "OLD_HOOK $ev after its dir was deleted: not exit 2 (non-blocking; exit $STEP_RC)" "stdout='$STEP_OUT'; stderr='$STEP_ERR'"
  fi
done
step "NEW_HOOK pre after the old dir was deleted" hook_call "$NEW_HOOK" pre "$PRE"
if not_block "$STEP_RC"; then
  check PASS "NEW_HOOK pre after old dir deleted: not a block (exit $STEP_RC)" "stdout='$STEP_OUT'; stderr='$STEP_ERR'"
else
  check FAIL "NEW_HOOK pre after old dir deleted: not a block (exit $STEP_RC)" "stdout='$STEP_OUT'; stderr='$STEP_ERR'"
fi
step "NEW_HOOK stop after the old dir was deleted" hook_call "$NEW_HOOK" stop "$STOP"
if not_block "$STEP_RC"; then
  check PASS "NEW_HOOK stop after old dir deleted: not a block (exit $STEP_RC)" "stdout='$STEP_OUT'; stderr='$STEP_ERR'"
else
  check FAIL "NEW_HOOK stop after old dir deleted: not a block (exit $STEP_RC)" "stdout='$STEP_OUT'; stderr='$STEP_ERR'"
fi

# --- 7. finish the run through the TO launcher ----------------------------------
rm -f "$SB_BIN/pawl"
ln -s "$NEW_LAUNCHER" "$SB_BIN/pawl"
step "NEW launcher: pawl status (via PATH symlink)" sb pawl status
if [ "$STEP_RC" -eq 0 ] && printf '%s' "$STEP_OUT" | grep -q "$RUN_ID"; then
  check PASS "$TO status lists the run started under $FROM" "$(printf '%s' "$STEP_OUT" | head -n3)"
else
  check FAIL "$TO status lists the run started under $FROM" "rc=$STEP_RC out=$STEP_OUT err=$STEP_ERR"
fi
step "NEW launcher: pawl status --run $RUN_ID --json" sb pawl status --run "$RUN_ID" --json
step "NEW launcher: pawl run $WF (resume)" sb PAWL_ENFORCEMENT=off pawl run "$WF"
if printf '%s' "$STEP_OUT" | grep -q "^ASK $RUN_ID"; then
  check PASS "$TO resume of the $FROM run re-presents the human step" "$(printf '%s' "$STEP_OUT" | grep '^ASK' | head -n1)"
else
  check FAIL "$TO resume of the $FROM run re-presents the human step" "rc=$STEP_RC out=$STEP_OUT err=$STEP_ERR"
fi
step "NEW launcher: pawl submit (answer: go)" sb pawl submit --run "$RUN_ID" --step ask --json '{"selected":["go"]}'
if [ "$STEP_RC" -eq 0 ] && printf '%s' "$STEP_OUT" | grep -q "TERMINAL $RUN_ID ok"; then
  check PASS "$FROM-started run reaches TERMINAL ok on $TO (journal written by $FROM decodes)" "$(printf '%s' "$STEP_OUT" | grep TERMINAL | head -n1)"
else
  check FAIL "$FROM-started run reaches TERMINAL ok on $TO (journal written by $FROM decodes)" "rc=$STEP_RC out=$STEP_OUT err=$STEP_ERR"
fi
step "events.jsonl after finishing on $TO" cat "$EV"
step "status --run after terminal" sb pawl status --run "$RUN_ID"
first_fmt="$(head -n1 "$EV" | jq -r '.format // "absent"')"
note "RUN_START format after the run finished on $TO: $first_fmt (a legacy run is not re-stamped)"
check PASS "observed: RUN_START format after finishing on $TO" "$first_fmt"

e2e_finish
