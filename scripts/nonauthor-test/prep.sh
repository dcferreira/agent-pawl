#!/usr/bin/env bash
# prep.sh - build the WP10 non-author-test sandbox (see README.md).
#
#   REF=main bash scripts/nonauthor-test/prep.sh                       # snapshot of a git ref (default)
#   SOURCE=local TO=<x.y.z> bash scripts/nonauthor-test/prep.sh        # a published release
#
# Environment: SOURCE=snapshot|local (default snapshot; live is refused), REF (snapshot: branch or
# tag of github.com/dcferreira/agent-pawl, default main), TO (local: the release version, required),
# E2E_TMP (where the sandbox lives, default $HOME/sandboxes).
#
# Everything the tester will see is built here, from release-style artefacts only: the plugin zip
# installed through the real `claude plugin` CLI (no repo clone) and a docs snapshot. The sandbox is
# ALWAYS kept (KEEP=1); teardown is manual (README.md). Prints ROOT=... and the verify result last.
# Manual tooling, not run in CI, no tests of its own. Reuses ../e2e-install/lib.sh; never edit it.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

MODE="${SOURCE:-snapshot}"
case "$MODE" in
  snapshot | local) ;;
  live)
    echo "error: SOURCE=live is refused: the live marketplace clones the WHOLE repo (source, DESIGN.md," >&2
    echo "       AGENTS.md, internal/, .git) into \$CLAUDE_CONFIG_DIR/plugins/marketplaces/, so the tester" >&2
    echo "       would have the source one 'ls' away. Use SOURCE=snapshot (REF=main) or SOURCE=local TO=x.y.z." >&2
    exit 2
    ;;
  *) echo "error: SOURCE must be snapshot or local, got '$MODE'" >&2; exit 2 ;;
esac
if [ "$MODE" = local ]; then
  [ -n "${TO:-}" ] || { echo "error: SOURCE=local needs TO=<x.y.z>" >&2; exit 2; }
  TO="${TO#v}"
fi
REF="${REF:-main}"
[ "$MODE" = snapshot ] && [ -n "${TO:-}" ] && echo "note: TO is ignored in snapshot mode (the version is derived); use SOURCE=local TO=x.y.z for a release" >&2
export KEEP=1
export E2E_TMP="${E2E_TMP:-$HOME/sandboxes}"
mkdir -p "$E2E_TMP"
E2E_TMP="$(cd "$E2E_TMP" && pwd -P)"
for t in curl jq unzip zip tar git sha256sum go bwrap; do
  command -v "$t" >/dev/null 2>&1 || { echo "error: required tool '$t' not found" >&2; exit 2; }
done

# --- staging: OUTSIDE the sandbox root, removed on exit -------------------------------------------
STAGE="$(mktemp -d "${TMPDIR:-/tmp}/wp10-stage.XXXXXX")"
# shellcheck disable=SC2317 # reached only via the EXIT trap handlers below
rm_stage() {
  if [ -n "${STAGE:-}" ] && [ -d "$STAGE" ]; then
    chmod -R u+w "${STAGE:?}" 2>/dev/null || true
    rm -rf "${STAGE:?}"
  fi
}
# shellcheck disable=SC2317 # only ever invoked indirectly, as an EXIT trap handler
prep_exit() {
  local rc=$?
  rm_stage
  (exit "$rc")
  e2e_cleanup
}
# shellcheck disable=SC2317 # EXIT trap handler (invoked indirectly)
early_exit() {
  local rc=$?
  rm_stage
  exit "$rc"
}
trap early_exit EXIT

SRC="$STAGE/src"
COMMIT=""
if [ "$MODE" = snapshot ]; then
  # A fresh clone of GitHub: reproducible and VCS-neutral; never the local checkout.
  echo "== snapshot: cloning $REF"
  git clone -q --depth 1 --branch "$REF" https://github.com/dcferreira/agent-pawl "$SRC"
  COMMIT="$(git -C "$SRC" rev-parse HEAD)"
  base="$(jq -r .version "$SRC/.claude-plugin/plugin.json")"
  # Semver prerelease: distinct from every published release. pawl's version check
  # (internal/cli/version_check.go) is plain string equality between the binary's stamped version and
  # the plugin.json version, and bin/pawl exports any [A-Za-z0-9._+-] version, so a suffix is fine.
  TO="${base}-wp10.${COMMIT:0:7}"
  echo "== snapshot commit $COMMIT, version $TO"
  # Patch the STAGING clone only.
  jq --arg v "$TO" '.version = $v' "$SRC/.claude-plugin/plugin.json" >"$STAGE/plugin.json.new"
  cp "$STAGE/plugin.json.new" "$SRC/.claude-plugin/plugin.json"
  rm -f "$STAGE/plugin.json.new"
  gr_ver="$(sed -n 's/^[[:space:]]*version:[[:space:]]*\(v[0-9][0-9.]*\)[[:space:]]*$/\1/p' "$SRC/.github/workflows/release.yml" | head -n1)"
  [ -n "$gr_ver" ] || { echo "error: could not read the goreleaser version pinned in release.yml" >&2; exit 1; }
  echo "== goreleaser $gr_ver (snapshot build, stamped $TO)"
  # A temp copy of the config, with the snapshot version pinned to $TO (so archives are
  # pawl_<TO>_<os>_<arch>.tar.gz and the binaries are stamped with $TO).
  cp "$SRC/.goreleaser.yaml" "$STAGE/goreleaser.yaml"
  printf '\nsnapshot:\n  version_template: "%s"\n' "$TO" >>"$STAGE/goreleaser.yaml"
  (
    cd "$SRC"
    go run "github.com/goreleaser/goreleaser/v2@$gr_ver" release --snapshot --clean --skip=publish \
      --config "$STAGE/goreleaser.yaml"
  )
  PLUGIN_SRC="$SRC" bash "$SRC/scripts/release/build-plugin-archive.sh" "$TO" "$SRC/dist" "$STAGE/out"
else
  echo "== release: downloading v$TO source tarball"
  mkdir -p "$SRC"
  curl -fsSL -o "$STAGE/src.tar.gz" "https://github.com/dcferreira/agent-pawl/archive/refs/tags/v$TO.tar.gz"
  tar -xzf "$STAGE/src.tar.gz" -C "$SRC" --strip-components=1
fi

# --- sandbox (lib.sh) -----------------------------------------------------------------------------
SOURCE=local
# shellcheck source=../e2e-install/lib.sh
# shellcheck source-path=SCRIPTDIR
. "$HERE/../e2e-install/lib.sh"

if [ "$MODE" = snapshot ]; then
  # Same as lib.sh's make_local_marketplace (sha256 check, source rewritten to ./plugin), but fed from
  # the locally built $STAGE/out instead of downloading a release.
  make_local_marketplace() {
    local ver="${1#v}" dir="$2" zip want got
    zip="$STAGE/out/agent-pawl-plugin_$ver.zip"
    mkdir -p "$dir/.claude-plugin" "$dir/plugin"
    want="$(jq -r '.plugins[0].source.sha256 // empty' "$STAGE/out/marketplace.json")"
    got="$(sha256sum "$zip" | awk '{print $1}')"
    if [ -n "$want" ] && [ "$want" = "$got" ]; then
      check PASS "plugin zip sha256 matches marketplace.json" "$got"
    else
      check FAIL "plugin zip sha256 matches marketplace.json" "want=$want got=$got"
      return 1
    fi
    step "extract plugin zip" sb unzip -q "$zip" -d "$dir/plugin"
    [ "$STEP_RC" -eq 0 ] || return 1
    jq '.plugins[0].source = "./plugin"' "$STAGE/out/marketplace.json" >"$dir/.claude-plugin/marketplace.json"
  }
fi

e2e_init wp10
trap prep_exit EXIT # e2e_init installs its own trap; this one also removes $STAGE
install_plugin || die "plugin install failed (see $TRANSCRIPT)"
[ -n "${PLUGIN_DIR:-}" ] || die "PLUGIN_DIR not set"

# The local marketplace dir stays: known_marketplaces.json points at it. The downloaded
# zip/json copies lib.sh leaves directly under ROOT are not needed.
rm -f "${ROOT:?}"/agent-pawl-plugin_*.zip "${ROOT:?}"/marketplace-*.json

# --- docs snapshot: what a stranger reaches by following README.md links ---------------------------
DOCS="$SB_HOME/pawl-docs"
mkdir -p "$DOCS/design"
cp "$SRC/README.md" "$DOCS/README.md"
cp -R "$SRC/docs" "$DOCS/docs"
cp "$SRC/DESIGN.md" "$DOCS/DESIGN.md"
cp "$SRC/design/format-spec.md" "$DOCS/design/format-spec.md"
chmod -R u+w "$DOCS"
rm -rf "${STAGE:?}"
STAGE=""

# --- seed project ---------------------------------------------------------------------------------
# The seed's files are stored with a .seed suffix so repo-wide gofmt/vet/staticcheck skip the planted
# problems; strip the suffix on copy so the tester sees plain files.
cp -R "$HERE/seed/textstats" "$SB_HOME/textstats"
find "$SB_HOME/textstats" -type f -name '*.seed' -print0 | while IFS= read -r -d '' f; do mv "$f" "${f%.seed}"; done
cat >"$SB_HOME/.gitconfig" <<'GC'
[user]
	name = WP10 Tester
	email = tester@example.invalid
[init]
	defaultBranch = main
GC
SB_CWD="$SB_HOME/textstats"
sb git init -q
sb git add -A
sb git commit -q -m "textstats: initial import"
SB_CWD="$SB_WORK"

# --- brief + friction log -------------------------------------------------------------------------
sed "s/<TO>/$TO/g" "$HERE/brief.md" >"$SB_HOME/BRIEF.md"
cp "$HERE/friction-template.md" "$SB_HOME/FRICTION.md"

# --- settings: keep what the plugin install wrote, add the policy ---------------------------------
sf="$SB_CFG/settings.json"
[ -f "$sf" ] || echo '{}' >"$sf"
jq '.permissions.deny = ((.permissions.deny // []) + ["WebFetch", "WebSearch"] | unique)
    | .env.DISABLE_AUTOUPDATER = "1"
    | .skipDangerousModePermissionPrompt = true' "$sf" >"$sf.new"
mv "$sf.new" "$sf"

# --- shell init (no plugin bin dir on PATH: a stranger's terminal does not have it) ---------------
cat >"$SB_HOME/.bashrc" <<'RC'
# sandbox shell
PS1='\u@sandbox:\w\$ '
export PATH
RC
cat >"$SB_HOME/.profile" <<'PR'
[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"
PR

# --- claude binary: placeholder file inside ROOT, enter.sh ro-binds the real one onto it ----------
rm -f "$SB_BIN/claude"
: >"$SB_BIN/claude"
chmod 0755 "$SB_BIN/claude"

# --- kit.env (visible to the tester: no real-home paths) and ROOT.host.env (outside ROOT) ---------
go_dir_kit=""
case "$GO_DIR" in
  "$HOME"/* | "$HOME") die "go lives under the real HOME ($GO_DIR): it would be hidden in the sandbox" ;;
  *) go_dir_kit="$GO_DIR" ;;
esac
cat >"$ROOT/kit.env" <<KE
ROOT=$ROOT
SB_HOME=$SB_HOME
SB_CFG=$SB_CFG
SB_GOPATH=$SB_GOPATH
GO_DIR=$go_dir_kit
TO=$TO
PLUGIN_DIR=$PLUGIN_DIR
KE
cat >"$ROOT.host.env" <<HE
CLAUDE_BIN=$CLAUDE_BIN
REAL_HOME=$HOME
SOURCE_MODE=$MODE
SOURCE_REF=$REF
SOURCE_COMMIT=$COMMIT
HE

# --- verify ---------------------------------------------------------------------------------------
echo "ROOT=$ROOT"
rc=0
bash "$HERE/verify-isolation.sh" "$ROOT" || rc=$?
echo "ROOT=$ROOT"
if [ "$rc" -eq 0 ]; then echo "VERIFY: PASS (version $TO)"; else echo "VERIFY: FAIL (rc=$rc)"; fi
exit "$rc"
