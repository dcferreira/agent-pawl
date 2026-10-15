#!/usr/bin/env bash
# verify-isolation.sh - check, from INSIDE the sandbox, that the WP10 tester cannot reach anything it
# must not (see README.md). Run by prep.sh, and again right before launch.
#
#   bash scripts/nonauthor-test/verify-isolation.sh <ROOT>
#
# Prints PASS/FAIL (and INFO) per check; exits 1 if any check FAILs.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[ $# -eq 1 ] || { echo "usage: verify-isolation.sh <ROOT>" >&2; exit 2; }
ROOT="$(cd "$1" && pwd -P)"
# shellcheck source=/dev/null
. "$ROOT/kit.env"
# shellcheck source=/dev/null
. "$ROOT.host.env"
# Real-host facts, computed OUTSIDE (inside, $HOME is the sandbox). The kit's own repo root is the
# checkout (or workspace) this script lives in.
REAL_REPO="$(cd "$HERE/../.." && pwd -P)"
REAL_REPO_NAME="agentic-workflow-fsm"

INNER=$(
  cat <<'INNER_EOF'
ROOT="$1" REAL_HOME="$2" REAL_REPO="$3" TO="$4" PLUGIN_DIR="$5" REPO_NAME="$6"
fails=0
pass() { printf 'PASS: %s\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1"; fails=$((fails + 1)); }
info() { printf 'INFO: %s\n' "$1"; }
chk() { # chk "name" cmd... : PASS if cmd succeeds
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then pass "$name"; else fail "$name"; fi
}
under() { case "$1" in "$ROOT"/*) return 0 ;; *) return 1 ;; esac; }

# --- environment ---
under "$HOME" && pass "HOME under ROOT ($HOME)" || fail "HOME not under ROOT ($HOME)"
under "${CLAUDE_CONFIG_DIR:-/nonexistent}" && pass "CLAUDE_CONFIG_DIR under ROOT" || fail "CLAUDE_CONFIG_DIR not under ROOT"
bad=""
for v in TMUX TMUX_PANE SSH_AUTH_SOCK GH_TOKEN GITHUB_TOKEN ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN DBUS_SESSION_BUS_ADDRESS XDG_RUNTIME_DIR; do
  [ -n "${!v+x}" ] && bad="$bad $v"
done
[ -z "$bad" ] && pass "no forbidden env vars set" || fail "forbidden env vars set:$bad"
extra="$(env | cut -d= -f1 | grep -E '^(ANTHROPIC_|GH_|GITHUB_|SSH_|AWS_|OPENAI_)' || true)"
[ -z "$extra" ] && pass "no ANTHROPIC_*/GH_*/GITHUB_*/SSH_*/AWS_*/OPENAI_* variables" || fail "credential-looking variables: $extra"

# --- filesystem: /home shows only the path down to ROOT ---
parent="$(dirname "$ROOT")"
ok=1
walk="$ROOT"
case "$ROOT" in
  /home/*)
    cur=/home
    rest="${ROOT#/home/}"
    while [ -n "$rest" ]; do
      next="${rest%%/*}"
      listing="$(ls -A "$cur")"
      [ "$listing" = "$next" ] || { ok=0; printf '  %s contains: %s (expected only %s)\n' "$cur" "$(echo $listing)" "$next"; }
      cur="$cur/$next"
      [ "$rest" = "$next" ] && break
      rest="${rest#*/}"
    done
    ;;
  *) [ -z "$(ls -A /home)" ] || ok=0 ;;
esac
[ "$ok" = 1 ] && pass "/home shows only the path down to ROOT" || fail "/home exposes more than the path down to ROOT"
chk "real repo checkout absent ($REAL_REPO)" test ! -e "$REAL_REPO"
chk "real ~/Obsidian absent" test ! -e "$REAL_HOME/Obsidian"
chk "real ~/.claude absent" test ! -e "$REAL_HOME/.claude"
chk "real ~/.ssh and ~/.config absent" sh -c "test ! -e '$REAL_HOME/.ssh' && test ! -e '$REAL_HOME/.config'"
chk "/run/user absent" test ! -e /run/user
if [ -z "$(ls -A /tmp)" ]; then pass "/tmp is empty"; else fail "/tmp not empty: $(ls -A /tmp | head -5 | tr '\n' ' ')"; fi
chk "no tmux socket dir" sh -c 'ls -d /tmp/tmux-* 2>/dev/null | grep -q . && exit 1 || exit 0'
n="$(ls /proc | grep -c '^[0-9][0-9]*$' || true)"
[ "$n" -le 20 ] && pass "pid namespace is private ($n processes visible)" || fail "$n processes visible (pid namespace leaking?)"

# --- forbidden names ---
# AGENTS.md anywhere; DESIGN.md/format-spec.md anywhere except under ~/pawl-docs; the engine source dirs.
# Under ROOT a hit is a FAIL; elsewhere on the (read-only, system) filesystem it is INFO.
hits="$( { find / -xdev \( -path /proc -o -path /sys -o -path /dev \) -prune -o \
           \( -name AGENTS.md -o -name DESIGN.md -o -name format-spec.md -o -path '*/internal/engine' -o -path '*/cmd/pawl' \) -print 2>/dev/null
         find "$ROOT" \( -name AGENTS.md -o -name DESIGN.md -o -name format-spec.md -o -path '*/internal/engine' -o -path '*/cmd/pawl' \) -print 2>/dev/null
         { find / -xdev \( -path /proc -o -path /sys -o -path /dev \) -prune -o -name go.mod -print0 2>/dev/null
           find "$ROOT" -name go.mod -print0 2>/dev/null; } | xargs -0 -r grep -l -e 'module github.com/dcferreira/agent-pawl' 2>/dev/null || true; } | sort -u)"
bad=""; ok_docs=""; sysinfo=""
while IFS= read -r h; do
  [ -n "$h" ] || continue
  case "$h" in
    "$HOME/pawl-docs/DESIGN.md" | "$HOME/pawl-docs/design/format-spec.md") ok_docs="$ok_docs $h" ;;
    "$ROOT"/*) bad="$bad $h" ;;
    *) sysinfo="$sysinfo $h" ;;
  esac
done <<<"$hits"
[ -z "$bad" ] && pass "no forbidden source names under ROOT (AGENTS.md, internal/engine, cmd/pawl, pawl go.mod, stray DESIGN.md/format-spec.md)" || fail "forbidden names under ROOT:$bad"
[ -z "$sysinfo" ] || info "same names elsewhere on the system filesystem (not part of the repo?):$sysinfo"

# --- no host identifiers anywhere under ROOT ---
# The docs snapshot is published, unmodified repo content and legitimately mentions "Obsidian"
# (docs/examples/brain-dispatch is the maintainer's own setup), so that one pattern is scanned
# outside ~/pawl-docs and only reported as INFO inside it. Everything else is scanned everywhere.
for pat in "$REPO_NAME" "Obsidian" "$REAL_HOME/.claude"; do
  if [ "$pat" = Obsidian ]; then
    m="$(grep -rIl --exclude-dir=pawl-docs -e "$pat" "$ROOT" 2>/dev/null | head -5 | tr '\n' ' ')"
    nd="$(grep -rIl -e "$pat" "$HOME/pawl-docs" 2>/dev/null | wc -l)"
    [ "$nd" -eq 0 ] || info "'$pat' appears in $nd published doc file(s) under ~/pawl-docs (public content, not a leak)"
  else
    m="$(grep -rIl -e "$pat" "$ROOT" 2>/dev/null | head -5 | tr '\n' ' ')"
  fi
  [ -z "$m" ] && pass "grep '$pat' under ROOT: no matches" || fail "grep '$pat' under ROOT matches: $m"
done

# --- policy ---
sf="$CLAUDE_CONFIG_DIR/settings.json"
chk "settings.json denies WebFetch and WebSearch" jq -e '.permissions.deny | index("WebFetch") and index("WebSearch")' "$sf"
chk "settings.json env.DISABLE_AUTOUPDATER=1" jq -e '.env.DISABLE_AUTOUPDATER == "1"' "$sf"

# --- claude + plugin ---
cv="$(claude --version 2>&1)" && pass "claude --version works ($cv)" || fail "claude --version failed: $cv"
pl="$(claude plugin list 2>&1)" || true
if printf '%s\n' "$pl" | grep -q 'agent-pawl@agent-pawl' && printf '%s\n' "$pl" | grep -qE '^ *Status: .*enabled *$' && printf '%s\n' "$pl" | grep -qxF "    Version: $TO"; then
  pass "claude plugin list: agent-pawl enabled, version $TO"
else
  fail "claude plugin list does not show agent-pawl enabled at $TO: $(printf '%s' "$pl" | tr '\n' ' ')"
fi
ip="$CLAUDE_CONFIG_DIR/plugins/installed_plugins.json"
ipath="$(jq -r '[.plugins[][]?.installPath] | first // empty' "$ip" 2>/dev/null || true)"
[ -n "$ipath" ] && [ -d "$ipath" ] && under "$ipath" && pass "installed_plugins.json installPath exists under ROOT" || fail "installed_plugins.json installPath missing or outside ROOT ($ipath)"

# HARD version agreement: plugin.json == libexec binary == bin/pawl launcher == TO
os=linux; case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) arch=unknown ;; esac
pj="$(jq -r .version "$PLUGIN_DIR/.claude-plugin/plugin.json" 2>/dev/null || true)"
lb="$("$PLUGIN_DIR/libexec/${os}_$arch/pawl" version 2>&1 | awk 'NR==1 && $1=="pawl" {print $2}')"
ll="$("$PLUGIN_DIR/bin/pawl" version 2>&1 | awk 'NR==1 && $1=="pawl" {print $2}')"
if [ "$pj" = "$TO" ] && [ "$lb" = "$TO" ] && [ "$ll" = "$TO" ]; then
  pass "plugin.json ($pj) == libexec binary ($lb) == bin/pawl launcher ($ll) == TO"
else
  fail "version mismatch: plugin.json='$pj' libexec='$lb' launcher='$ll' TO='$TO'"
fi

# --- docs snapshot + seed ---
D="$HOME/pawl-docs"
chk "~/pawl-docs/README.md exists" test -f "$D/README.md"
chk "~/pawl-docs/docs/install.md exists" test -f "$D/docs/install.md"
chk "~/pawl-docs/DESIGN.md exists" test -f "$D/DESIGN.md"
chk "~/pawl-docs/design/format-spec.md exists" test -f "$D/design/format-spec.md"
extra="$(find "$D" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort | tr '\n' ' ')"
[ "$extra" = "DESIGN.md README.md design docs " ] && pass "~/pawl-docs top level is exactly README.md docs/ design/ DESIGN.md" || fail "~/pawl-docs top level: $extra"
[ "$(find "$D/design" -type f | wc -l)" = 1 ] && pass "~/pawl-docs/design holds only format-spec.md" || fail "~/pawl-docs/design holds more than format-spec.md"
chk "~/BRIEF.md says version $TO" grep -qF "its version is $TO" "$HOME/BRIEF.md"
chk "~/FRICTION.md present" test -s "$HOME/FRICTION.md"
chk "~/textstats is a git repo with one commit" sh -c "test \"\$(git -C $HOME/textstats rev-list --count HEAD)\" = 1"
(cd "$HOME/textstats" && go vet ./... >/dev/null 2>&1) && fail "textstats go vet unexpectedly passes" || pass "textstats go vet fails as planted (go works offline)"
chk "DNS resolves (api.anthropic.com)" getent hosts api.anthropic.com

echo "---"
[ "$fails" -eq 0 ] && echo "ISOLATION: PASS" || echo "ISOLATION: FAIL ($fails)"
[ "$fails" -eq 0 ]
INNER_EOF
)

echo "== verify-isolation: $ROOT"
exec "$HERE/enter.sh" "$ROOT" bash -c "$INNER" inner "$ROOT" "$REAL_HOME" "$REAL_REPO" "$TO" "$PLUGIN_DIR" "$REAL_REPO_NAME"
