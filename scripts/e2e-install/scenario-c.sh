#!/usr/bin/env bash
# Scenario C: plugin update FROM -> TO through the claude CLI, and what is left
# behind (old cache dir retention is recorded as an observed fact together with
# the claude CLI version: it is Claude Code behaviour and may change).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
# shellcheck source-path=SCRIPTDIR
. "$HERE/lib.sh"

e2e_init c
P="$(plat)"
step "claude --version (for the retention observations below)" sb claude --version
CLAUDE_VER="$(printf '%s' "$STEP_OUT" | head -n1)"

# --- 1. how does a user get FROM? ---------------------------------------
# (a) a git-ref marketplace pinned to the tag. Probed in a throwaway config dir
# so it cannot collide with the marketplace the scenario actually uses.
probe_cfg="$ROOT/cfg-tagprobe"
mkdir -p "$probe_cfg"
step "claude plugin marketplace add $REPO_SLUG#v$FROM (separate throwaway config dir)" \
  sb "CLAUDE_CONFIG_DIR=$probe_cfg" claude plugin marketplace add "$REPO_SLUG#v$FROM"
tag_rc="$STEP_RC"
tag_mj="$(find "$probe_cfg/plugins/marketplaces" -name marketplace.json -path '*.claude-plugin*' 2>/dev/null | head -n1)"
tag_zip=""
[ -n "$tag_mj" ] && tag_zip="$(jq -r '.plugins[0].source.url // empty' "$tag_mj")"
if [ "$tag_rc" -eq 0 ] && [ -n "$tag_mj" ]; then
  note "marketplace.json at tag v$FROM names: $tag_zip"
  case "$tag_zip" in
    */v$FROM/agent-pawl-plugin_$FROM.zip)
      check PASS "git-ref marketplace at tag v$FROM points at the $FROM zip" "$tag_zip" ;;
    *)
      check SKIP "git-ref marketplace at tag v$FROM points at the $FROM zip" "observed: it points at $tag_zip (the release flow bumps marketplace.json after tagging, so a tag-pinned marketplace installs the PREVIOUS release); falling back to a sandbox marketplace serving the v$FROM release marketplace.json" ;;
  esac
else
  check SKIP "git-ref marketplace at tag v$FROM" "marketplace add $REPO_SLUG#v$FROM failed (exit $tag_rc): $(printf '%s' "$STEP_ERR$STEP_OUT" | head -c 200)"
fi
note "SHA refs (marketplace add $REPO_SLUG#<commit>) were also tried by hand while writing this harness: 'Remote branch <sha> not found in upstream origin' - the ref must be a branch or tag."

# (b) fallback / method actually used.
install_plugin_version "$FROM" || { check FAIL "FROM plugin installed" "see transcript"; e2e_finish; exit $?; }
check PASS "method used to install $FROM" "$INSTALL_METHOD"
note "METHOD: $INSTALL_METHOD"

# --- 2. FROM is what is installed ---------------------------------------
from_real="$PLUGIN_DIR/libexec/$P/pawl"
got="$(version_of "$from_real")"
if [ "$got" = "$FROM" ]; then
  check PASS "installed libexec binary reports $FROM" "$got"
else
  check FAIL "installed libexec binary reports $FROM" "got '$got'"
fi
step "installed launcher: bin/pawl version" sb "$PLUGIN_DIR/bin/pawl" version
got="$(printf '%s\n' "$STEP_OUT" | awk 'NR==1 && $1=="pawl" {print $2}')"
if [ "$got" = "$FROM" ]; then
  check PASS "installed launcher reports $FROM" "$got"
else
  check FAIL "installed launcher reports $FROM" "got '$got'"
fi
step "installed_plugins.json before update" cat "$SB_CFG/plugins/installed_plugins.json"
step "settings.json before update" cat "$SB_CFG/settings.json"

# --- 3. update -----------------------------------------------------------
update_plugin_to "$TO" || { check FAIL "update $FROM -> $TO" "see transcript"; e2e_finish; exit $?; }
check PASS "update path used" "advance the marketplace (git commit of the v$TO release marketplace.json), then: claude plugin marketplace update; claude plugin update agent-pawl@agent-pawl"

# --- 4. TO is what is installed ------------------------------------------
to_real="$PLUGIN_DIR/libexec/$P/pawl"
got="$(version_of "$to_real")"
if [ "$got" = "$TO" ]; then
  check PASS "new libexec binary reports $TO" "$got"
else
  check FAIL "new libexec binary reports $TO" "got '$got'"
fi
step "updated launcher: bin/pawl version" sb "$PLUGIN_DIR/bin/pawl" version
got="$(printf '%s\n' "$STEP_OUT" | awk 'NR==1 && $1=="pawl" {print $2}')"
if [ "$got" = "$TO" ]; then
  check PASS "updated launcher reports $TO" "$got"
else
  check FAIL "updated launcher reports $TO" "got '$got'"
fi
ln -s "$PLUGIN_DIR/bin/pawl" "$SB_BIN/pawl"
assert_pawl_in_sandbox
step "pawl version via PATH symlink to the new launcher" sb pawl version
if printf '%s\n' "$STEP_OUT" | grep -q "^pawl $TO\$"; then
  check PASS "PATH symlink to the new launcher reports $TO" "$STEP_OUT"
else
  check FAIL "PATH symlink to the new launcher reports $TO" "$STEP_OUT"
fi

step "installed_plugins.json after update" cat "$SB_CFG/plugins/installed_plugins.json"
ip_ver="$(jq -r '.plugins["agent-pawl@agent-pawl"][0].version // empty' "$SB_CFG/plugins/installed_plugins.json")"
ip_path="$(jq -r '.plugins["agent-pawl@agent-pawl"][0].installPath // empty' "$SB_CFG/plugins/installed_plugins.json")"
if [ "$ip_ver" = "$TO" ] && [ "$ip_path" = "$PLUGIN_DIR" ]; then
  check PASS "installed_plugins.json records $TO at the new cache dir" "$ip_ver $ip_path"
else
  check FAIL "installed_plugins.json records $TO at the new cache dir" "version=$ip_ver installPath=$ip_path want=$PLUGIN_DIR"
fi
step "settings.json after update" cat "$SB_CFG/settings.json"
step "known_marketplaces.json after update" cat "$SB_CFG/plugins/known_marketplaces.json"
step "claude plugin list" sb claude plugin list

# --- 5. old cache dir retention (observed fact, with the claude version) --
step "ls -la plugin cache after update" ls -la "$SB_CFG/plugins/cache/agent-pawl/agent-pawl/"
if [ -d "$OLD_PLUGIN_DIR" ]; then
  r1=kept
  step "ls -la of the old $FROM dir (any orphan marker?)" ls -la "$OLD_PLUGIN_DIR"
  [ -f "$OLD_PLUGIN_DIR/.orphaned_at" ] && step "cat old dir .orphaned_at" cat "$OLD_PLUGIN_DIR/.orphaned_at"
else
  r1=deleted
fi
check PASS "observed: old $FROM cache dir right after update: $r1" "claude $CLAUDE_VER; $OLD_PLUGIN_DIR"
step "second unrelated claude invocation: claude plugin list" sb claude plugin list
step "third: claude plugin marketplace list" sb claude plugin marketplace list
if [ -d "$OLD_PLUGIN_DIR" ]; then r2=kept; else r2=deleted; fi
check PASS "observed: old $FROM cache dir after a second claude invocation: $r2" "claude $CLAUDE_VER; $OLD_PLUGIN_DIR"
note "OBSERVED FACT (claude $CLAUDE_VER): after 'claude plugin update', the old $FROM cache dir was $r1 immediately and $r2 after further claude CLI calls. This is Claude Code behaviour and may differ in other releases."

# --- 6. what removing the marketplace does (the 'switch source' question) --
step "claude plugin marketplace remove agent-pawl" sb claude plugin marketplace remove agent-pawl
step "claude plugin list after marketplace remove" sb claude plugin list
if sb claude plugin list 2>&1 | grep -q 'agent-pawl@agent-pawl'; then
  check PASS "observed: removing the marketplace uninstalls the plugin: no" "claude $CLAUDE_VER"
else
  check PASS "observed: removing the marketplace uninstalls the plugin: yes (so remove+re-add is NOT a no-downtime way to switch marketplace source)" "claude $CLAUDE_VER; see 'marketplace remove' transcript"
fi
if [ -d "$PLUGIN_DIR" ]; then r3=kept; else r3=deleted; fi
check PASS "observed: current $TO cache dir after marketplace remove/uninstall: $r3" "claude $CLAUDE_VER"
if [ "$SOURCE" = live ]; then
  step "claude plugin marketplace add $REPO_SLUG (live)" sb claude plugin marketplace add "$REPO_SLUG"
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "live marketplace add after removal" "exit $STEP_RC"
  step "claude plugin install agent-pawl@agent-pawl (live)" sb claude plugin install agent-pawl@agent-pawl
  live_ver="$(jq -r '.plugins["agent-pawl@agent-pawl"][0].version // empty' "$SB_CFG/plugins/installed_plugins.json")"
  if [ "$live_ver" = "$TO" ]; then
    check PASS "live marketplace serves $TO (fresh install reports $TO)" "installed_plugins.json version $live_ver"
  else
    check FAIL "live marketplace serves $TO" "installed version '$live_ver'; $STEP_OUT $STEP_ERR"
  fi
else
  check SKIP "live marketplace serves $TO" "SOURCE=local: rc not on the live marketplace"
fi

e2e_finish
