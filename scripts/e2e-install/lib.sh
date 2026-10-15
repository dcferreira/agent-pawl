#!/usr/bin/env bash
# lib.sh - shared helpers for the install/update verification harness.
# Sourced by run.sh-driven scenario-*.sh; never executed directly. Manual use
# only (not CI), no tests of its own. Needs network (github.com,
# proxy.golang.org), curl, jq, unzip, tar, git, sha256sum, go, and claude on
# the caller's PATH.
#
# SAFETY MODEL
#   Every scenario gets its own mktemp dir ($ROOT) holding home/ claude-config/
#   bin/ gopath/ work/. Everything under test runs through `sb`, which uses
#   `env -i` with only HOME, CLAUDE_CONFIG_DIR, GOPATH, GOMODCACHE, GOCACHE,
#   GOFLAGS(-modcacherw, so the module cache can be deleted), TERM=dumb and a
#   PATH of <root>/bin + <extra dirs> + /usr/bin:/bin. No credentials, nothing
#   copied from the real ~/.claude, no proxy variables. Cleanup is exactly
#   `rm -rf "${ROOT:?}"` of the scenario's own mktemp dir (skipped if KEEP=1).
#
# PARAMETERS (environment; defaults applied by e2e_defaults)
#   FROM=0.7.0   TO=0.8.0   SOURCE=live|local   REPORT_DIR   KEEP=1
#   E2E_TMP   base dir for scenario roots (default ${TMPDIR:-/tmp})
#
# HELPER API
#   e2e_init <scenario>        set up sandbox, guards, trap, transcript header
#   e2e_finish                 print scenario result; return 1 if any check FAILed
#   sb [VAR=val ...] cmd...    run cmd in the sandbox env (cwd $SB_CWD, default
#                              $ROOT/work); leading VAR=val words are added to env
#   SB_EXTRA_PATH              bash array of extra PATH dirs (must be under $ROOT
#                              or a toolchain dir); set e.g. SB_EXTRA_PATH=("$d")
#   step "desc" cmd...         run cmd, append command/exit code/stdout/stderr
#                              verbatim to the transcript; sets STEP_RC, STEP_OUT,
#                              STEP_ERR; never aborts the scenario
#   note "text"                append a prose line to the transcript
#   check PASS|FAIL|SKIP "name" "detail"   record a result in summary.md + transcript
#   die "msg"                  abort the scenario (exit 2, cleanup still runs)
#   assert_pawl_in_sandbox     abort unless `command -v pawl` inside sb is under $ROOT
#   plat                       linux_amd64 style platform string for libexec/
#   fetch <url> <dest>         plain curl download run inside the sandbox env
#   version_of <pawl-path>     print the X.Y.Z that `<path> version` reports
#   release_url <ver> <asset>  https://github.com/dcferreira/agent-pawl/releases/download/v<ver>/<asset>
#   make_local_marketplace <ver> <dir>   directory marketplace from a release's own
#                              marketplace.json + plugin zip (rc mode)
#   install_plugin             marketplace add + plugin install (live or local per
#                              $SOURCE); sets PLUGIN_DIR to the installed cache dir
#   fetch_source_tree <ver> <dir>   extract the v<ver> source tag into <dir>
#   set_marketplace_version <ver>   (re)write the sandbox git-repo marketplace ($MP_DIR) so it
#                              serves release v<ver>: the release's own marketplace.json,
#                              UNMODIFIED (archive source -> a real cache copy), committed
#   plugin_dir_for <ver>       print the cache dir of installed agent-pawl <ver> (empty if none)
#   install_plugin_version <ver>   marketplace add ($MP_DIR) + plugin install of that release;
#                              sets PLUGIN_DIR, INSTALL_METHOD
#   update_plugin_to <ver>     advance $MP_DIR to <ver>, `claude plugin marketplace update` +
#                              `claude plugin update agent-pawl@agent-pawl`; sets OLD_PLUGIN_DIR
#                              (the dir before) and PLUGIN_DIR (the dir after); returns 1 on failure
#   fetch_release_binary <ver> <dest-file>   download + checksum-verify the release tarball's pawl
#   ver_ge <A> <B>             true when version A >= B (X.Y.Z; see the function)
#   PAWL_FIRST_HEARTBEAT_CHECK  first release that has PR #75's heartbeat-based plugin check
# Globals after e2e_init: SCEN ROOT SB_HOME SB_CFG SB_BIN SB_GOPATH SB_WORK
#   REPORT_DIR TRANSCRIPT SUMMARY CLAUDE_BIN GO_DIR

REPO_SLUG="dcferreira/agent-pawl"
E2E_FAILS=0

e2e_defaults() {
  FROM="${FROM:-0.7.0}"
  TO="${TO:-0.8.0}"
  SOURCE="${SOURCE:-live}"
  FROM="${FROM#v}"
  TO="${TO#v}"
  case "$SOURCE" in live | local) ;; *) echo "error: SOURCE must be live or local, got '$SOURCE'" >&2; exit 2 ;; esac
  E2E_TMP="${E2E_TMP:-${TMPDIR:-/tmp}}"
}

die() {
  echo "ABORT [${SCEN:-?}]: $*" >&2
  [ -n "${TRANSCRIPT:-}" ] && printf '\nABORT: %s\n' "$*" >>"$TRANSCRIPT"
  exit 2
}

_realpath() { (cd "$1" 2>/dev/null && pwd -P); }

e2e_cleanup() {
  local rc=$?
  if [ -n "${ROOT:-}" ] && [ -d "$ROOT" ]; then
    if [ "${KEEP:-0}" = "1" ]; then
      echo "KEEP=1: sandbox left at $ROOT"
    else
      case "$ROOT" in
        */pawl-e2e-*)
          chmod -R u+w "${ROOT:?}" 2>/dev/null || true
          rm -rf "${ROOT:?}"
          ;;
        *) echo "refusing to remove unexpected path: $ROOT" >&2 ;;
      esac
    fi
  fi
  return "$rc"
}

# e2e_init <scenario-letter-or-name>
e2e_init() {
  SCEN="$1"
  e2e_defaults
  local tool
  for tool in curl jq unzip tar git sha256sum env mktemp; do
    command -v "$tool" >/dev/null 2>&1 || die "required tool '$tool' not found"
  done

  if [ -z "${REPORT_DIR:-}" ]; then
    mkdir -p "$E2E_TMP"
    REPORT_DIR="$(mktemp -d "$E2E_TMP/pawl-e2e-report.XXXXXX")"
    echo "REPORT_DIR=$REPORT_DIR"
  fi
  mkdir -p "$REPORT_DIR"
  TRANSCRIPT="$REPORT_DIR/$SCEN.transcript.md"
  SUMMARY="$REPORT_DIR/summary.md"

  mkdir -p "$E2E_TMP"
  ROOT="$(mktemp -d "$E2E_TMP/pawl-e2e-$SCEN.XXXXXX")"
  ROOT="$(_realpath "$ROOT")"
  trap e2e_cleanup EXIT
  SB_HOME="$ROOT/home"
  SB_CFG="$ROOT/claude-config"
  SB_BIN="$ROOT/bin"
  SB_GOPATH="$ROOT/gopath"
  SB_WORK="$ROOT/work"
  mkdir -p "$SB_HOME" "$SB_CFG" "$SB_BIN" "$SB_GOPATH" "$SB_WORK" "$ROOT/gocache"
  SB_EXTRA_PATH=()
  SB_CWD="$SB_WORK"

  _abort_guard

  # Resolve claude and go once, from the caller's PATH.
  local c
  c="$(command -v claude)" || die "claude not found on PATH"
  CLAUDE_BIN="$(readlink -f "$c")"
  ln -s "$CLAUDE_BIN" "$SB_BIN/claude"
  if g="$(command -v go)"; then
    GO_DIR="$(dirname "$(readlink -f "$g")")"
    case "$GO_DIR" in /usr/bin | /bin) ;; *) SB_EXTRA_PATH+=("$GO_DIR") ;; esac
  else
    GO_DIR=""
  fi

  {
    printf '# Scenario %s transcript\n\n' "$SCEN"
    printf -- '- date (UTC): %s\n- FROM=%s TO=%s SOURCE=%s\n- sandbox root: %s (removed on exit unless KEEP=1)\n' \
      "$(date -u +%FT%TZ)" "$FROM" "$TO" "$SOURCE" "$ROOT"
    printf -- '- host: %s\n- sandbox env: env -i HOME CLAUDE_CONFIG_DIR GOPATH GOMODCACHE GOCACHE GOFLAGS TERM=dumb PATH (no other variables)\n\n' "$(uname -srm)"
  } >"$TRANSCRIPT"
  step "claude --version" sb claude --version
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "claude version recorded" "${STEP_OUT:-}"
}

# Hard abort if the sandbox could alias the real home / claude config.
_abort_guard() {
  local real_home real_claude="" sb_home sb_cfg
  real_home="$(_realpath "${HOME:-/nonexistent}")" || real_home=""
  [ -n "${CLAUDE_CONFIG_DIR:-}" ] && real_claude="$(_realpath "$CLAUDE_CONFIG_DIR")"
  sb_home="$(_realpath "$SB_HOME")"
  sb_cfg="$(_realpath "$SB_CFG")"
  case "$ROOT" in "" | "/") die "bad sandbox root '$ROOT'" ;; esac
  [ -n "$real_home" ] && [ "$sb_home" = "$real_home" ] && die "sandbox HOME is the real HOME"
  [ -n "$real_home" ] && [ "$sb_cfg" = "$real_home/.claude" ] && die "sandbox CLAUDE_CONFIG_DIR is the real ~/.claude"
  [ -n "$real_home" ] && [ "$sb_cfg" = "$real_home" ] && die "sandbox CLAUDE_CONFIG_DIR is the real HOME"
  [ -n "$real_claude" ] && [ "$sb_cfg" = "$real_claude" ] && die "sandbox CLAUDE_CONFIG_DIR is the real CLAUDE_CONFIG_DIR"
  case "$sb_home" in "$ROOT"/*) ;; *) die "sandbox HOME not under root" ;; esac
  case "$sb_cfg" in "$ROOT"/*) ;; *) die "sandbox CLAUDE_CONFIG_DIR not under root" ;; esac
  return 0
}

# sb [VAR=val ...] cmd args...  -- run in the sandbox environment.
sb() {
  local -a extra=()
  while [ $# -gt 0 ] && [[ $1 =~ ^[A-Za-z_][A-Za-z0-9_]*= ]]; do
    extra+=("$1")
    shift
  done
  _abort_guard
  local p="$SB_BIN" d
  for d in "${SB_EXTRA_PATH[@]}"; do p="$p:$d"; done
  p="$p:/usr/bin:/bin"
  (
    cd "${SB_CWD:-$SB_WORK}" || exit 97
    exec env -i \
      HOME="$SB_HOME" \
      CLAUDE_CONFIG_DIR="$SB_CFG" \
      GOPATH="$SB_GOPATH" \
      GOMODCACHE="$SB_GOPATH/pkg/mod" \
      GOCACHE="$ROOT/gocache" \
      GOFLAGS=-modcacherw \
      TERM=dumb \
      PATH="$p" \
      "${extra[@]}" \
      "$@"
  )
}

assert_pawl_in_sandbox() {
  local out
  out="$(sb sh -c 'command -v pawl')" || die "no pawl on the sandbox PATH"
  case "$(readlink -f "$out")" in
    "$ROOT"/*) ;;
    *) die "sandbox 'pawl' resolves outside the root: $out" ;;
  esac
  case "$out" in "$ROOT"/*) ;; *) die "sandbox PATH 'pawl' is outside the root: $out" ;; esac
  note "sandbox \`command -v pawl\` = $out (under root: ok)"
}

step() {
  local desc="$1"
  shift
  local o e
  o="$(mktemp "$ROOT/.step-out.XXXXXX")"
  e="$(mktemp "$ROOT/.step-err.XXXXXX")"
  if "$@" >"$o" 2>"$e"; then STEP_RC=0; else STEP_RC=$?; fi
  STEP_OUT="$(cat "$o")"
  STEP_ERR="$(cat "$e")"
  {
    printf '\n### %s\n\n````\n$ ' "$desc"
    printf '%q ' "$@"
    # shellcheck disable=SC2016 # literal markdown fences/backticks in the transcript, not expansions
    printf '\n````\n\nexit code: %s\n\nstdout:\n\n````\n%s\n````\n\nstderr:\n\n````\n%s\n````\n' \
      "$STEP_RC" "$STEP_OUT" "$STEP_ERR"
  } >>"$TRANSCRIPT"
  rm -f "$o" "$e"
  echo "  [$SCEN] $desc -> exit $STEP_RC"
  return 0
}

note() {
  printf '\n%s\n' "$*" >>"$TRANSCRIPT"
}

check() {
  local status="$1" name="$2" detail="${3:-}"
  case "$status" in PASS | FAIL | SKIP) ;; *) die "check: bad status '$status'" ;; esac
  [ "$status" = FAIL ] && E2E_FAILS=$((E2E_FAILS + 1))
  if [ ! -f "$SUMMARY" ]; then
    printf '# Install/update harness summary\n\n| status | scenario | check | detail |\n|---|---|---|---|\n' >"$SUMMARY"
  fi
  local d
  d="$(printf '%s' "$detail" | tr '\n' ' ' | sed 's/|/\\|/g' | cut -c1-300)"
  printf '| %s | %s | %s | %s |\n' "$status" "$SCEN" "$name" "$d" >>"$SUMMARY"
  printf '\n**CHECK %s** - %s - %s\n' "$status" "$name" "$detail" >>"$TRANSCRIPT"
  echo "  [$SCEN] $status: $name"
}

e2e_finish() {
  echo "[$SCEN] done: $E2E_FAILS failed check(s)"
  [ "$E2E_FAILS" -eq 0 ]
}

plat() {
  local os arch
  case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) os=unknown ;; esac
  case "$(uname -m)" in x86_64 | amd64) arch=amd64 ;; arm64 | aarch64) arch=arm64 ;; *) arch=unknown ;; esac
  echo "${os}_${arch}"
}

release_url() {
  echo "https://github.com/$REPO_SLUG/releases/download/v${1#v}/$2"
}

fetch() {
  sb curl -fsSL -o "$2" "$1"
}

# version_of <path-to-pawl>: prints X.Y.Z from `pawl X.Y.Z` (empty on failure).
version_of() {
  sb "$1" version 2>/dev/null | awk 'NR==1 && $1=="pawl" {print $2}'
}

fetch_source_tree() {
  local ver="${1#v}" dir="$2"
  mkdir -p "$dir"
  step "download source tag v$ver" fetch "https://github.com/$REPO_SLUG/archive/refs/tags/v$ver.tar.gz" "$ROOT/src-$ver.tar.gz"
  [ "$STEP_RC" -eq 0 ] || return 1
  step "extract source tag v$ver" sb tar -xzf "$ROOT/src-$ver.tar.gz" -C "$dir" --strip-components=1
  [ "$STEP_RC" -eq 0 ]
}

# make_local_marketplace <ver> <dir>: a directory marketplace built from the
# release's own marketplace.json and plugin zip, for versions (rcs) that never
# reach the live marketplace. The archive source is rewritten to the extracted
# zip as a relative path; the zip's sha256 is verified against the published one.
make_local_marketplace() {
  local ver="${1#v}" dir="$2" zip want got
  mkdir -p "$dir/.claude-plugin" "$dir/plugin"
  step "download v$ver marketplace.json" fetch "$(release_url "$ver" marketplace.json)" "$ROOT/marketplace-$ver.json"
  [ "$STEP_RC" -eq 0 ] || return 1
  zip="$ROOT/agent-pawl-plugin_$ver.zip"
  step "download plugin zip v$ver" fetch "$(release_url "$ver" "agent-pawl-plugin_$ver.zip")" "$zip"
  [ "$STEP_RC" -eq 0 ] || return 1
  want="$(jq -r '.plugins[0].source.sha256 // empty' "$ROOT/marketplace-$ver.json")"
  got="$(sha256sum "$zip" | awk '{print $1}')"
  if [ -n "$want" ] && [ "$want" = "$got" ]; then
    check PASS "plugin zip sha256 matches marketplace.json" "$got"
  else
    check FAIL "plugin zip sha256 matches marketplace.json" "want=$want got=$got"
    return 1
  fi
  step "extract plugin zip" sb unzip -q "$zip" -d "$dir/plugin"
  [ "$STEP_RC" -eq 0 ] || return 1
  jq '.plugins[0].source = "./plugin"' "$ROOT/marketplace-$ver.json" >"$dir/.claude-plugin/marketplace.json"
  note "local marketplace written: $dir/.claude-plugin/marketplace.json (source rewritten to ./plugin)"
}

# install_plugin: marketplace add + plugin install, from the live marketplace
# or (SOURCE=local) a directory marketplace for $TO. Sets PLUGIN_DIR.
install_plugin() {
  local src="$REPO_SLUG"
  if [ "$SOURCE" = local ]; then
    make_local_marketplace "$TO" "$ROOT/local-marketplace" || return 1
    src="$ROOT/local-marketplace"
  fi
  step "claude plugin marketplace add $src" sb claude plugin marketplace add "$src"
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "marketplace add ($SOURCE)" "exit $STEP_RC"
  [ "$STEP_RC" -eq 0 ] || return 1
  step "claude plugin install agent-pawl@agent-pawl" sb claude plugin install agent-pawl@agent-pawl
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "plugin install ($SOURCE)" "exit $STEP_RC"
  [ "$STEP_RC" -eq 0 ] || return 1
  local base="$SB_CFG/plugins/cache" d
  PLUGIN_DIR=""
  for d in "$base"/*/agent-pawl/*/; do
    [ -f "$d/.claude-plugin/plugin.json" ] && PLUGIN_DIR="${d%/}"
  done
  if [ -z "$PLUGIN_DIR" ]; then
    check FAIL "installed plugin dir located" "nothing under $base"
    return 1
  fi
  note "installed plugin dir: $PLUGIN_DIR"
  return 0
}

# PAWL_FIRST_HEARTBEAT_CHECK: the first release that contains PR #75 (`pawl hook
# pre` records plugin_version in its heartbeat, `pawl run` refuses when it differs
# from its own version, and the launcher exports PAWL__LAUNCHER for the
# `submit with:` lines). The fix was unreleased when this was written and is
# EXPECTED to ship in 0.9.0: if it ships in a different release, correct this
# value (scenario F's F5 checks gate on it).
# shellcheck disable=SC2034 # read by scenario-f.sh
PAWL_FIRST_HEARTBEAT_CHECK="0.9.0"

# ver_ge <A> <B>: succeeds when version A >= version B. Both are X.Y.Z with an
# optional leading "v"; a pre-release suffix ("-rc1", anything after the first
# "-") is IGNORED for ordering, so 0.9.0-rc1 compares equal to 0.9.0 (a release
# candidate carries the fixes of the release it precedes). Missing components
# count as 0; a non-numeric component compares as 0.
ver_ge() {
  local a="${1#v}" b="${2#v}" i x y
  a="${a%%-*}"
  b="${b%%-*}"
  local -a pa pb
  IFS=. read -r -a pa <<<"$a"
  IFS=. read -r -a pb <<<"$b"
  for i in 0 1 2; do
    x="${pa[$i]:-0}"
    y="${pb[$i]:-0}"
    case "$x" in '' | *[!0-9]*) x=0 ;; esac
    case "$y" in '' | *[!0-9]*) y=0 ;; esac
    [ "$((10#$x))" -gt "$((10#$y))" ] && return 0
    [ "$((10#$x))" -lt "$((10#$y))" ] && return 1
  done
  return 0
}

# ---- marketplace-advancing install/update (scenarios C and E) --------------
#
# Models "the live marketplace moved from FROM to TO" without touching the
# network marketplace: $MP_DIR is a sandbox git repo whose .claude-plugin/
# marketplace.json is the release's own file (archive source, absolute zip URL,
# sha256), so Claude Code makes a real cache copy exactly as for the live
# marketplace. (A relative-path source, as make_local_marketplace writes, is
# read in place and never cached - useless for update/retention questions.)

MP_DIR=""
INSTALL_METHOD=""
OLD_PLUGIN_DIR=""

set_marketplace_version() {
  local ver="${1#v}"
  MP_DIR="${MP_DIR:-$ROOT/marketplace}"
  mkdir -p "$MP_DIR/.claude-plugin"
  if [ ! -f "$ROOT/marketplace-$ver.json" ]; then
    step "download v$ver marketplace.json" fetch "$(release_url "$ver" marketplace.json)" "$ROOT/marketplace-$ver.json"
    [ "$STEP_RC" -eq 0 ] || return 1
  fi
  cp "$ROOT/marketplace-$ver.json" "$MP_DIR/.claude-plugin/marketplace.json"
  if [ ! -d "$MP_DIR/.git" ]; then
    step "git init sandbox marketplace" sb git -C "$MP_DIR" init -q
  fi
  step "git add marketplace.json (v$ver)" sb git -C "$MP_DIR" add -A
  step "git commit marketplace.json (v$ver)" sb git -C "$MP_DIR" -c user.name=e2e -c user.email=e2e@example.invalid commit -q --allow-empty -m "marketplace v$ver"
  note "marketplace $MP_DIR now serves: $(jq -r '.plugins[0].source.url' "$MP_DIR/.claude-plugin/marketplace.json")"
}

plugin_dir_for() {
  local d
  for d in "$SB_CFG"/plugins/cache/*/agent-pawl/"$1"; do
    [ -f "$d/.claude-plugin/plugin.json" ] && { echo "$d"; return 0; }
  done
  return 1
}

install_plugin_version() {
  local ver="${1#v}"
  set_marketplace_version "$ver" || return 1
  # shellcheck disable=SC2034 # read by the scenarios that source this file
  INSTALL_METHOD="sandbox git-repo directory marketplace serving the v$ver release marketplace.json (archive source)"
  step "claude plugin marketplace add $MP_DIR" sb claude plugin marketplace add "$MP_DIR"
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "marketplace add (v$ver marketplace)" "exit $STEP_RC"
  [ "$STEP_RC" -eq 0 ] || return 1
  step "claude plugin install agent-pawl@agent-pawl (v$ver)" sb claude plugin install agent-pawl@agent-pawl
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "plugin install v$ver" "exit $STEP_RC"
  [ "$STEP_RC" -eq 0 ] || return 1
  PLUGIN_DIR="$(plugin_dir_for "$ver")" || PLUGIN_DIR=""
  if [ -z "$PLUGIN_DIR" ]; then
    check FAIL "installed plugin dir for v$ver located" "nothing at $SB_CFG/plugins/cache/*/agent-pawl/$ver"
    return 1
  fi
  note "installed plugin dir: $PLUGIN_DIR"
}

update_plugin_to() {
  local ver="${1#v}"
  OLD_PLUGIN_DIR="$PLUGIN_DIR"
  set_marketplace_version "$ver" || return 1
  step "claude plugin marketplace update" sb claude plugin marketplace update
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "marketplace update (-> v$ver)" "exit $STEP_RC"
  step "claude plugin update agent-pawl@agent-pawl" sb claude plugin update agent-pawl@agent-pawl
  check "$([ "$STEP_RC" -eq 0 ] && echo PASS || echo FAIL)" "plugin update (-> v$ver)" "exit $STEP_RC: $STEP_OUT $STEP_ERR"
  [ "$STEP_RC" -eq 0 ] || return 1
  PLUGIN_DIR="$(plugin_dir_for "$ver")" || PLUGIN_DIR=""
  if [ -z "$PLUGIN_DIR" ]; then
    check FAIL "updated plugin dir for v$ver located" "nothing at $SB_CFG/plugins/cache/*/agent-pawl/$ver"
    return 1
  fi
  note "updated plugin dir: $PLUGIN_DIR (previous: $OLD_PLUGIN_DIR)"
}

# fetch_release_binary <ver> <dest-file>: the release tarball for this platform,
# verified against the release's checksums.txt; extracts just `pawl` to dest.
fetch_release_binary() {
  local ver="${1#v}" dest="$2" asset want got dl="$ROOT/dl-$1"
  mkdir -p "$dl"
  asset="pawl_${ver}_$(plat).tar.gz"
  step "download $asset" fetch "$(release_url "$ver" "$asset")" "$dl/$asset"
  [ "$STEP_RC" -eq 0 ] || return 1
  step "download checksums.txt (v$ver)" fetch "$(release_url "$ver" checksums.txt)" "$dl/checksums.txt"
  [ "$STEP_RC" -eq 0 ] || return 1
  want="$(awk -v a="$asset" '$2==a {print $1}' "$dl/checksums.txt")"
  got="$(sha256sum "$dl/$asset" | awk '{print $1}')"
  if [ -z "$want" ] || [ "$want" != "$got" ]; then
    check FAIL "release tarball v$ver checksum" "want=$want got=$got"
    return 1
  fi
  check PASS "release tarball v$ver checksum matches checksums.txt" "$got"
  step "extract pawl from $asset" sb tar -xzf "$dl/$asset" -C "$dl" pawl
  [ "$STEP_RC" -eq 0 ] || return 1
  mv "$dl/pawl" "$dest"
  chmod +x "$dest"
}
