#!/usr/bin/env bash
# Unit tests for scripts/release/check-fragment.sh,
# check-no-version-bump.sh, check-version-consistency.sh and
# build-plugin-archive.sh — run with
# `make test-release-checks` or `bash scripts/release/test-checks.sh`.
#
# Each script is SOURCED (not executed) with PAWL_RELEASE_CHECK_TEST=1, the
# same pattern scripts/test-install.sh uses for install.sh, so their
# functions become callable without going through git diff plumbing by
# hand for every case, and their own main()/argv guard doesn't fire.
#
# The two diff-based checks are exercised against real throwaway git repos
# built fresh per test in a temp dir (like a bats "fixture", just without
# bats — this repo's no-extra-tooling ethos, same as test-install.sh). No
# network, no GitHub API: PR_LABELS/PR_HEAD_REF are plain env vars, base and
# head are real commit shas in the fixture repo.
set -euo pipefail

cd "$(dirname "$0")/../.."
REPO_ROOT="$(pwd)"

export PAWL_RELEASE_CHECK_TEST=1
# shellcheck source=/dev/null
source ./scripts/release/check-fragment.sh
# shellcheck source=/dev/null
source ./scripts/release/check-no-version-bump.sh
# shellcheck source=/dev/null
source ./scripts/release/check-version-consistency.sh
# shellcheck source=/dev/null
source ./scripts/release/build-plugin-archive.sh

failures=0
tests_run=0

# assert_ok DESCRIPTION -- COMMAND...
assert_ok() {
  tests_run=$((tests_run + 1))
  local desc="$1"
  shift
  if "$@" >/tmp/release-check-test-out.$$ 2>&1; then
    echo "ok: $desc"
  else
    echo "FAIL: $desc (expected pass, got failure)"
    sed 's/^/    /' /tmp/release-check-test-out.$$
    failures=$((failures + 1))
  fi
  rm -f /tmp/release-check-test-out.$$
}

# assert_fails DESCRIPTION -- COMMAND...
assert_fails() {
  tests_run=$((tests_run + 1))
  local desc="$1"
  shift
  if "$@" >/tmp/release-check-test-out.$$ 2>&1; then
    echo "FAIL: $desc (expected failure, got pass)"
    sed 's/^/    /' /tmp/release-check-test-out.$$
    failures=$((failures + 1))
  else
    echo "ok: $desc"
  fi
  rm -f /tmp/release-check-test-out.$$
}

# --- fixture repo builder ------------------------------------------------
#
# Every fixture starts with a "base" commit that has a real .changie.yaml
# and a plugin.json at version 0.1.0 with a .changes/v0.1.0.md, so the
# bootstrap path is never accidentally exercised by tests that aren't
# specifically testing bootstrap.

FIXTURE_DIR=""

new_fixture() {
  FIXTURE_DIR="$(mktemp -d)"
  (
    cd "$FIXTURE_DIR"
    git init -q
    git config user.email test@example.com
    git config user.name "Release Check Tests"
    mkdir -p .changes/unreleased .claude-plugin
    cat >.changie.yaml <<'EOF'
changesDir: .changes
unreleasedDir: unreleased
kinds:
  - label: Breaking
    key: breaking
    auto: minor
  - label: Added
    key: added
    auto: minor
  - label: Fixed
    key: fixed
    auto: patch
EOF
    cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "0.1.0"
}
EOF
    cat >.changes/v0.1.0.md <<'EOF'
## v0.1.0 - 2026-01-01

### Added

* Initial release.
EOF
    git add -A
    git commit -q -m "base"
  )
}

fixture_git() {
  git -C "$FIXTURE_DIR" "$@"
}

base_sha() {
  fixture_git rev-parse base
}

head_sha() {
  fixture_git rev-parse HEAD
}

# ==========================================================================
echo "== check-fragment.sh =="

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >.changes/unreleased/added-1.yaml <<'EOF'
kind: added
body: something
EOF
git add -A && git commit -q -m "add fragment")
assert_ok "fragment added -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-fragment.sh' && check_fragment \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && echo "unrelated" >README.md && git add -A && git commit -q -m "no fragment")
assert_fails "no fragment, no label, no release branch -> fail" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-fragment.sh' && check_fragment \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && echo "unrelated" >README.md && git add -A && git commit -q -m "no fragment")
assert_ok "no fragment but 'skip changelog' label -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 PR_LABELS='bug,skip changelog' && source '$REPO_ROOT/scripts/release/check-fragment.sh' && check_fragment \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && echo "unrelated" >README.md && git add -A && git commit -q -m "no fragment")
assert_ok "no fragment but newline-separated labels include skip -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 PR_LABELS=\$'bug\nskip changelog' && source '$REPO_ROOT/scripts/release/check-fragment.sh' && check_fragment \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && echo "unrelated" >README.md && git add -A && git commit -q -m "consume fragments")
assert_ok "no fragment but head ref is release/v* -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 PR_HEAD_REF='release/v0.2.0' && source '$REPO_ROOT/scripts/release/check-fragment.sh' && check_fragment \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
(cd "$FIXTURE_DIR" && rm .changie.yaml && git add -A && git commit -q -m "no changie yet")
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >.changie.yaml <<'EOF'
changesDir: .changes
unreleasedDir: unreleased
EOF
git add -A && git commit -q -m "introduce changie, no fragment yet")
assert_ok "bootstrap: .changie.yaml missing at base -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-fragment.sh' && check_fragment \$(git rev-parse base) \$(git rev-parse HEAD)"

# --- kind validation: fragment `kind:` must be a key in .changie.yaml ----
# changie matches `kind:` against the kind KEY (`added`), not the label
# (`Added`); a mismatch passes the "fragment added" check but breaks
# `changie batch` at release time.
cf_run() { # cf_run BASE_TAG HEAD_TAG [ENV...]
  local b="$1" h="$2"
  shift 2
  env "$@" PAWL_RELEASE_CHECK_TEST=1 bash -c "cd '$FIXTURE_DIR' && source '$REPO_ROOT/scripts/release/check-fragment.sh' && check_fragment \$(git rev-parse $b) \$(git rev-parse $h)"
}

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && printf 'kind: Added\nbody: x\n' >.changes/unreleased/label.yaml && git add -A && git commit -q -m frag)
assert_fails "kind: Added (label, not key) -> fail" cf_run base HEAD
tests_run=$((tests_run + 1))
if out=$(cf_run base HEAD 2>&1) || true; echo "$out" | grep -q "\.changes/unreleased/label.yaml" && echo "$out" | grep -q "'Added'" && echo "$out" | grep -q "valid: breaking, added, fixed"; then
  echo "ok: error names the file, the bad kind and the valid keys"
else
  echo "FAIL: error should name file, bad kind and valid keys; got: $out"
  failures=$((failures + 1))
fi

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && printf 'kind: "fixed"\nbody: x\n' >.changes/unreleased/q.yaml && git add -A && git commit -q -m frag)
assert_ok "quoted valid kind key -> pass" cf_run base HEAD

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && printf 'kind: nonsense\nbody: x\n' >.changes/unreleased/n.yaml && git add -A && git commit -q -m frag)
assert_fails "unknown kind -> fail" cf_run base HEAD

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && printf 'body: x\n' >.changes/unreleased/nokind.yaml && git add -A && git commit -q -m frag)
assert_fails "fragment with no kind: line -> fail" cf_run base HEAD

new_fixture
(cd "$FIXTURE_DIR" && printf 'kind: Added\nbody: x\n' >.changes/unreleased/old.yaml && git add -A && git commit -q -m "bad fragment already on base")
fixture_git tag base
(cd "$FIXTURE_DIR" && echo unrelated >README.md && git add -A && git commit -q -m "PR: no fragment")
assert_fails "skip-changelog PR, pre-existing bad-kind fragment at head -> fail" cf_run base HEAD "PR_LABELS=skip changelog"

new_fixture
(cd "$FIXTURE_DIR" && printf 'kind: Added\nbody: x\n' >.changes/unreleased/old.yaml && git add -A && git commit -q -m "bad fragment")
fixture_git tag base
(cd "$FIXTURE_DIR" && echo unrelated >README.md && git add -A && git commit -q -m "release")
assert_ok "release/v* head ref skips the kind check too -> pass" cf_run base HEAD PR_HEAD_REF=release/v0.2.0

new_fixture
(cd "$FIXTURE_DIR" && rm .changie.yaml && git add -A && git commit -q -m "no changie yet")
fixture_git tag base
(cd "$FIXTURE_DIR" && printf 'changesDir: .changes\n' >.changie.yaml && printf 'kind: Added\nbody: x\n' >.changes/unreleased/b.yaml && git add -A && git commit -q -m "introduce changie")
assert_ok "bootstrap with a bad-kind fragment -> still pass" cf_run base HEAD

# base advances after the PR branches: a release merged into base deletes
# .changes/unreleased/*.yaml fragments other PRs had added before the PR
# branched. A stale PR that adds none of its own must still fail — a
# two-dot base-vs-head diff would see that deletion-on-base as "added" by
# this PR (absent at base's new tip, present at the PR's head, since the PR
# never deleted it) and let it slide.
new_fixture
(cd "$FIXTURE_DIR" && cat >.changes/unreleased/other-pr.yaml <<'EOF'
kind: added
body: someone else's change
EOF
git add -A && git commit -q -m "an earlier PR's fragment, already on base")
fixture_git tag pr-point
(cd "$FIXTURE_DIR" && echo "unrelated" >README.md && git add -A && git commit -q -m "PR branch: no fragment of its own")
fixture_git tag head
(cd "$FIXTURE_DIR" && git checkout -q pr-point && rm .changes/unreleased/other-pr.yaml && cat >CHANGELOG.md <<'EOF'
whatever
EOF
git add -A && git commit -q -m "release commit lands on base" && git tag base && git checkout -q -)
assert_fails "base advances (release deletes fragments) after PR branches, PR adds none of its own -> fail" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-fragment.sh' && check_fragment \$(git rev-parse base) \$(git rev-parse head)"

echo
echo "== check-no-version-bump.sh =="

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >.changes/unreleased/added-1.yaml <<'EOF'
kind: added
body: something
EOF
git add -A && git commit -q -m "normal PR")
assert_ok "normal PR, nothing touched -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && echo "# Changelog" >CHANGELOG.md && git add -A && git commit -q -m "hand-edit changelog")
assert_fails "PR touches CHANGELOG.md -> fail" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >.changes/v0.2.0.md <<'EOF'
## v0.2.0 - 2026-02-01
EOF
git add -A && git commit -q -m "hand-add release notes")
assert_fails "PR adds a .changes/v*.md -> fail" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "0.2.0"
}
EOF
git add -A && git commit -q -m "hand-bump version")
assert_fails "PR bumps plugin.json version -> fail" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture, renamed",
  "version": "0.1.0"
}
EOF
git add -A && git commit -q -m "unrelated plugin.json edit")
assert_ok "PR edits plugin.json but not .version -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >.changes/unreleased/added-1.yaml <<'EOF'
kind: added
body: fix a typo in my own fragment
EOF
git add -A && git commit -q -m "add fragment")
fixture_git tag base2
(cd "$FIXTURE_DIR" && rm .changes/unreleased/added-1.yaml && git add -A && git commit -q -m "delete my own fragment")
assert_ok "PR deletes its own unreleased fragment -> pass (editing/deleting fragments is allowed)" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base2) \$(git rev-parse HEAD)"

new_fixture
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >CHANGELOG.md <<'EOF'
whatever
EOF
git add -A && git commit -q -m "release PR batches")
assert_ok "release/v* head ref exempts touching CHANGELOG.md -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 PR_HEAD_REF='release/v0.2.0' && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse HEAD)"

new_fixture
(cd "$FIXTURE_DIR" && rm .changie.yaml && git add -A && git commit -q -m "no changie yet")
fixture_git tag base
(cd "$FIXTURE_DIR" && cat >.changie.yaml <<'EOF'
changesDir: .changes
unreleasedDir: unreleased
EOF
git add -A && git commit -q -m "introduce changie")
assert_ok "bootstrap: .changie.yaml missing at base -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse HEAD)"

# base advances after the PR branches: a release lands on base after the PR
# branched, touching CHANGELOG.md, adding a .changes/vX.md and bumping
# plugin.json's version — none of which is this PR's doing. A two-dot
# base-vs-head diff would see main's release changes "in reverse" (as if
# the PR itself reverted/touched them) and fail the PR; the merge-base form
# must still pass it.
new_fixture
fixture_git tag pr-point
(cd "$FIXTURE_DIR" && cat >.changes/unreleased/added-1.yaml <<'EOF'
kind: added
body: something
EOF
git add -A && git commit -q -m "PR branch: unrelated fragment only")
fixture_git tag head
(cd "$FIXTURE_DIR" && git checkout -q pr-point && cat >CHANGELOG.md <<'EOF'
whatever
EOF
cat >.changes/v0.2.0.md <<'EOF'
## v0.2.0 - 2026-02-01
EOF
rm -f .changes/unreleased/*.yaml
cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "0.2.0"
}
EOF
git add -A && git commit -q -m "release commit lands on base" && git tag base && git checkout -q -)
assert_ok "base advances (release commit) after PR branches -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse head)"

echo
echo "== check-version-consistency.sh =="

new_fixture
assert_ok "plugin.json version matches latest .changes/v*.md -> pass" check_version_consistency "$FIXTURE_DIR"

new_fixture
(cd "$FIXTURE_DIR" && cat >.changes/v0.2.0.md <<'EOF'
## v0.2.0 - 2026-02-01
EOF
git add -A && git commit -q -m "release notes for 0.2.0 without bumping plugin.json")
assert_fails "latest release note ahead of plugin.json -> fail" check_version_consistency "$FIXTURE_DIR"

new_fixture
(cd "$FIXTURE_DIR" && cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "9.9.9"
}
EOF
git add -A && git commit -q -m "plugin.json ahead of release notes")
assert_fails "plugin.json ahead of latest release note -> fail" check_version_consistency "$FIXTURE_DIR"

new_fixture
(cd "$FIXTURE_DIR" && rm -rf .changes && git add -A && git commit -q -m "no release notes at all")
assert_fails "no .changes/v*.md at all -> fail" check_version_consistency "$FIXTURE_DIR"

new_fixture
(cd "$FIXTURE_DIR" && cat >.changes/v0.10.0.md <<'EOF'
## v0.10.0 - 2026-03-01
EOF
cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "0.10.0"
}
EOF
git add -A && git commit -q -m "double digit minor sorts correctly")
assert_ok "v0.10.0 sorts after v0.1.0 (sort -V, not lexical) -> pass" check_version_consistency "$FIXTURE_DIR"

new_fixture
(cd "$FIXTURE_DIR" && cat >.changes/v1.0.0.md <<'EOF'
## v1.0.0 - 2026-04-01
EOF
cat >.changes/v1.0.0-rc1.md <<'EOF'
## v1.0.0-rc1 - 2026-03-15
EOF
cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "1.0.0"
}
EOF
git add -A && git commit -q -m "a prerelease file next to its final release")
assert_ok "prerelease file (v1.0.0-rc1.md) sorts before its final release, v1.0.0 is latest -> pass" check_version_consistency "$FIXTURE_DIR"

new_fixture
(cd "$FIXTURE_DIR" && cat >.changes/v0.3.0.md <<'EOF'
## v0.3.0 - 2026-02-15
EOF
cat >.changes/v1.0.0-rc1.md <<'EOF'
## v1.0.0-rc1 - 2026-03-15
EOF
cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "1.0.0-rc1"
}
EOF
git add -A && git commit -q -m "prerelease-only Release PR, no final release for its base version yet")
assert_ok "no v1.0.0 release yet, v1.0.0-rc1 is latest (prerelease-only cut) -> pass" check_version_consistency "$FIXTURE_DIR"

new_fixture
(cd "$FIXTURE_DIR" && cat >.changes/v1.0.0-rc1.md <<'EOF'
## v1.0.0-rc1 - 2026-03-01
EOF
cat >.changes/v1.0.0-rc2.md <<'EOF'
## v1.0.0-rc2 - 2026-03-15
EOF
cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "1.0.0-rc2"
}
EOF
git add -A && git commit -q -m "two prereleases of the same base version")
assert_ok "v1.0.0-rc2 sorts after v1.0.0-rc1, rc2 is latest -> pass" check_version_consistency "$FIXTURE_DIR"

# --- build-plugin-archive.sh --------------------------------------------
#
# Fixtures: a fake plugin source tree (only the files the plugin zip takes)
# and a fake goreleaser dist/ whose four tarballs each hold a one-line
# "pawl" script naming its platform, plus a matching checksums.txt.

PLUGIN_PLATFORMS="darwin_amd64 darwin_arm64 linux_amd64 linux_arm64"

# new_plugin_src DIR VERSION
new_plugin_src() {
  local d="$1" v="$2"
  mkdir -p "$d/.claude-plugin" "$d/hooks" "$d/skills/pawl" "$d/bin"
  printf '{"name":"agent-pawl","version":"%s"}\n' "$v" >"$d/.claude-plugin/plugin.json"
  echo '{"hooks":{}}' >"$d/hooks/hooks.json"
  echo '# skill' >"$d/skills/pawl/SKILL.md"
  printf '#!/bin/sh\necho launcher\n' >"$d/bin/pawl"
  printf '#!/bin/sh\necho hook\n' >"$d/bin/pawl-hook"
  chmod 0644 "$d/bin/pawl" "$d/bin/pawl-hook" # the zip must still ship them 0755
  # An unrelated file the zip must NOT pick up.
  echo junk >"$d/README.md"
}

# new_dist DIR VERSION
new_dist() {
  local d="$1" v="$2" plat stage
  mkdir -p "$d"
  : >"$d/checksums.txt"
  for plat in $PLUGIN_PLATFORMS; do
    stage="$(mktemp -d)"
    printf '#!/bin/sh\necho pawl %s\n' "$plat" >"$stage/pawl"
    chmod 0644 "$stage/pawl" # tar mode must not be trusted
    tar -czf "$d/pawl_${v}_${plat}.tar.gz" -C "$stage" pawl
    rm -rf "$stage"
    (cd "$d" && sha256sum "pawl_${v}_${plat}.tar.gz") >>"$d/checksums.txt"
  done
}

EXPECTED_ZIP_FILES=".claude-plugin/plugin.json
bin/pawl
bin/pawl-hook
hooks/hooks.json
libexec/darwin_amd64/pawl
libexec/darwin_arm64/pawl
libexec/linux_amd64/pawl
libexec/linux_arm64/pawl
skills/pawl/SKILL.md"

PLUGIN_TMP="$(mktemp -d)"
PSRC="$PLUGIN_TMP/src"
PDIST="$PLUGIN_TMP/dist"
POUT="$PLUGIN_TMP/out"
new_plugin_src "$PSRC" 1.2.3
new_dist "$PDIST" 1.2.3

# Runs the script in a subshell so `set -e`/exit paths can't kill the test run.
run_build() { (PLUGIN_SRC="$PSRC" build_plugin_archive "$@"); }

assert_ok "build-plugin-archive: happy path succeeds" run_build 1.2.3 "$PDIST" "$POUT"
ZIP="$POUT/agent-pawl-plugin_1.2.3.zip"
assert_ok "zip and marketplace.json are written" test -f "$ZIP" -a -f "$POUT/marketplace.json"

listing="$(unzip -Z1 "$ZIP" 2>/dev/null | grep -v '/$' | LC_ALL=C sort || true)"
assert_ok "zip contains exactly the contract's file list" test "$listing" = "$EXPECTED_ZIP_FILES"

modes_bad="$(unzip -Z "$ZIP" 2>/dev/null | awk '/^[-d]r/ && $1 !~ /^-rwxr-xr-x/ && $NF !~ /\/$/ && ($NF ~ /^libexec\// || $NF ~ /^bin\//) {print}' || true)"
assert_ok "libexec binaries and bin/* are mode 0755 in the zip" test -z "$modes_bad" -a "$(unzip -Z "$ZIP" | grep -c -- '-rwxr-xr-x')" -eq 6

x="$(mktemp -d)"
unzip -q "$ZIP" -d "$x"
assert_ok "libexec/linux_arm64/pawl is the linux_arm64 tarball's binary" grep -q 'pawl linux_arm64' "$x/libexec/linux_arm64/pawl"
assert_ok "skills/pawl/SKILL.md is a real file, not a symlink" test -f "$x/skills/pawl/SKILL.md" -a ! -L "$x/skills/pawl/SKILL.md" -a ! -L "$x/skills/pawl"
rm -rf "$x"

want_sha="$(sha256sum "$ZIP" | awk '{print $1}')"
want_url="https://github.com/dcferreira/agent-pawl/releases/download/v1.2.3/agent-pawl-plugin_1.2.3.zip"
assert_ok "marketplace.json sha256 equals sha256sum of the zip" test "$(jq -r '.plugins[0].source.sha256' "$POUT/marketplace.json")" = "$want_sha"
assert_ok "marketplace.json url matches the contract" test "$(jq -r '.plugins[0].source.url' "$POUT/marketplace.json")" = "$want_url"
assert_ok "marketplace.json shape (archive source, no version, owner, name)" jq -e '.name=="agent-pawl" and .owner.name=="Daniel Ferreira" and .plugins[0].name=="agent-pawl" and .plugins[0].source.source=="archive" and (.plugins[0]|has("version")|not)' "$POUT/marketplace.json"

if command -v claude >/dev/null 2>&1; then
  cfg="$(mktemp -d)"
  assert_ok "marketplace.json passes claude plugin validate" env CLAUDE_CONFIG_DIR="$cfg" claude plugin validate "$POUT/marketplace.json"
  rm -rf "$cfg"
else
  echo "skip: claude not on PATH, not validating marketplace.json"
fi

# Leading v: normalised (the release workflow's VERSION carries one).
POUT_V="$PLUGIN_TMP/out_v"
assert_ok "version with leading v is normalised" run_build v1.2.3 "$PDIST" "$POUT_V"
assert_ok "leading-v run writes the same zip name and url" test -f "$POUT_V/agent-pawl-plugin_1.2.3.zip" -a "$(jq -r '.plugins[0].source.url' "$POUT_V/marketplace.json")" = "$want_url"

# Checksum mismatch.
BAD="$PLUGIN_TMP/dist_bad"
cp -r "$PDIST" "$BAD"
echo tampered >>"$BAD/pawl_1.2.3_linux_amd64.tar.gz"
assert_fails "checksum mismatch fails" run_build 1.2.3 "$BAD" "$PLUGIN_TMP/out_bad"
assert_ok "checksum mismatch writes no zip" test ! -e "$PLUGIN_TMP/out_bad/agent-pawl-plugin_1.2.3.zip"

# Missing archive.
MISS="$PLUGIN_TMP/dist_miss"
cp -r "$PDIST" "$MISS"
rm "$MISS/pawl_1.2.3_darwin_arm64.tar.gz"
assert_fails "missing archive fails" run_build 1.2.3 "$MISS" "$PLUGIN_TMP/out_miss"

# Missing checksum line.
NOSUM="$PLUGIN_TMP/dist_nosum"
cp -r "$PDIST" "$NOSUM"
grep -v darwin_amd64 "$PDIST/checksums.txt" >"$NOSUM/checksums.txt"
assert_fails "missing checksum line fails" run_build 1.2.3 "$NOSUM" "$PLUGIN_TMP/out_nosum"

# plugin.json version != requested version.
assert_fails "plugin.json version != <ver> is a hard failure" run_build 1.2.4 "$PDIST" "$PLUGIN_TMP/out_ver"

# Missing version argument.
assert_fails "no version argument fails" run_build

rm -rf "$PLUGIN_TMP"

echo
echo "$tests_run tests run, $failures failed"
if [ "$failures" -ne 0 ]; then
  exit 1
fi
