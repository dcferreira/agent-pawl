#!/usr/bin/env bash
# Unit tests for scripts/release/check-fragment.sh,
# check-no-version-bump.sh, check-version-consistency.sh,
# build-plugin-archive.sh, update-repo-marketplace.sh, is-prerelease.sh and
# batch.sh — run with `make test-release-checks` or
# `bash scripts/release/test-checks.sh`.
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
#
# batch.sh's argument validation is tested without changie (it validates
# before invoking it); its end-to-end release-candidate proof drives a real
# changie against a fixture repo, so it needs the pinned changie (v1.26.0) on
# PATH. Without one the proof is skipped with a `skip:` line, unless
# PAWL_REQUIRE_CHANGIE=1 (CI sets it, like PAWL_REQUIRE_VCS_TOOLS for git),
# which turns a missing changie into a FAIL. This script never fetches
# changie itself — no network.
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
# shellcheck source=/dev/null
source ./scripts/release/update-repo-marketplace.sh
# shellcheck source=/dev/null
source ./scripts/release/is-prerelease.sh
# shellcheck source=/dev/null
source ./scripts/release/batch.sh

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
assert_ok "release/v1.0.0-rc.1 head ref exempts touching CHANGELOG.md -> pass" bash -c "cd '$FIXTURE_DIR' && export PAWL_RELEASE_CHECK_TEST=1 PR_HEAD_REF='release/v1.0.0-rc.1' && source '$REPO_ROOT/scripts/release/check-no-version-bump.sh' && check_no_version_bump \$(git rev-parse base) \$(git rev-parse HEAD)"

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

new_fixture
(cd "$FIXTURE_DIR" && for v in 1.0.0-rc.2 1.0.0; do printf '## v%s - 2026-03-15\n' "$v" >".changes/v$v.md"; done
cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "1.0.0"
}
EOF
git add -A && git commit -q -m "dotted prerelease next to its final release")
assert_ok "dotted v1.0.0-rc.2 sorts before v1.0.0, v1.0.0 is latest -> pass" check_version_consistency "$FIXTURE_DIR"
assert_ok "latest_version_from_changes picks v1.0.0 over v1.0.0-rc.2" test "$(latest_version_from_changes "$FIXTURE_DIR")" = "v1.0.0"

new_fixture
(cd "$FIXTURE_DIR" && for v in 1.0.0-rc.9 1.0.0-rc.10; do printf '## v%s - 2026-03-15\n' "$v" >".changes/v$v.md"; done
cat >.claude-plugin/plugin.json <<'EOF'
{
  "name": "fixture",
  "version": "1.0.0-rc.10"
}
EOF
git add -A && git commit -q -m "rc.9 and rc.10")
assert_ok "v1.0.0-rc.10 sorts after v1.0.0-rc.9 (numeric) -> pass" check_version_consistency "$FIXTURE_DIR"
assert_ok "latest_version_from_changes picks v1.0.0-rc.10 over v1.0.0-rc.9" test "$(latest_version_from_changes "$FIXTURE_DIR")" = "v1.0.0-rc.10"

# --- marketplace.json check (only when the file exists) -------------------
SHA_A=1d61b40641b1d81b677635686b62194ec03de24041047a273f84607897625604
write_fixture_marketplace() { # DIR SOURCE_JSON
  jq -n --argjson src "$2" '{name:"agent-pawl",plugins:[{name:"agent-pawl",source:$src}]}' >"$1/.claude-plugin/marketplace.json"
}
archive_src() { # VERSION SHA
  jq -n --arg v "$1" --arg s "$2" '{source:"archive",url:("https://github.com/dcferreira/agent-pawl/releases/download/v"+$v+"/agent-pawl-plugin_"+$v+".zip"),sha256:$s}'
}

new_fixture
write_fixture_marketplace "$FIXTURE_DIR" "$(archive_src 0.1.0 $SHA_A)"
assert_ok "marketplace archive entry for a released version -> pass" check_version_consistency "$FIXTURE_DIR"

new_fixture
(cd "$FIXTURE_DIR" && cat >.changes/v0.2.0.md <<'EOF'
## v0.2.0 - 2026-02-01
EOF
  sed -i 's/0.1.0/0.2.0/' .claude-plugin/plugin.json)
write_fixture_marketplace "$FIXTURE_DIR" "$(archive_src 0.1.0 $SHA_A)"
assert_ok "marketplace one release behind plugin.json (release PR window) -> pass" check_version_consistency "$FIXTURE_DIR"

new_fixture
write_fixture_marketplace "$FIXTURE_DIR" '"./"'
assert_fails "marketplace entry is not an archive source -> fail" check_version_consistency "$FIXTURE_DIR"

new_fixture
write_fixture_marketplace "$FIXTURE_DIR" "$(archive_src 0.9.0 $SHA_A)"
assert_fails "marketplace points at a version with no .changes/v*.md -> fail" check_version_consistency "$FIXTURE_DIR"

new_fixture
write_fixture_marketplace "$FIXTURE_DIR" "$(archive_src 0.1.0 deadbeef)"
assert_fails "marketplace sha256 is not 64 hex -> fail" check_version_consistency "$FIXTURE_DIR"

new_fixture
write_fixture_marketplace "$FIXTURE_DIR" '{"source":"archive","url":"https://example.com/agent-pawl-plugin_0.1.0.zip","sha256":"'$SHA_A'"}'
assert_fails "marketplace url is not the release asset url -> fail" check_version_consistency "$FIXTURE_DIR"

new_fixture
jq -n '{name:"agent-pawl",plugins:[{name:"other",source:"./"}]}' >"$FIXTURE_DIR/.claude-plugin/marketplace.json"
assert_fails "marketplace has no agent-pawl plugin -> fail" check_version_consistency "$FIXTURE_DIR"

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

# --- update-repo-marketplace.sh -----------------------------------------
echo
echo "== update-repo-marketplace.sh =="

UM_TMP="$(mktemp -d)"
SHA_B=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
um_release() { # FILE SOURCE_JSON
  jq -n --argjson src "$2" '{name:"agent-pawl",description:"release desc",plugins:[{name:"agent-pawl",description:"plugin desc",source:$src}]}' >"$1"
}
um_repo() { # SOURCE_JSON FILE
  jq -n --argjson src "$1" '{name:"agent-pawl",description:"repo desc",owner:{name:"Daniel Ferreira"},plugins:[{name:"agent-pawl",source:$src},{name:"other",description:"keep me",source:"./other"}]}' >"$2"
}
run_um() { (update_repo_marketplace "$@"); }
is_pretty() { [ "$(cat "$1")" = "$(jq . "$1")" ] && [ -z "$(tail -c1 "$1")" ] && [ "$(tail -c2 "$1" | od -An -tx1 | tr -d ' ')" != "0a0a" ]; }

um_release "$UM_TMP/rel.json" "$(archive_src 0.6.0 $SHA_A)"

um_repo '"./"' "$UM_TMP/repo1.json"
assert_ok "replaces a './' source with the release archive source" run_um "$UM_TMP/rel.json" "$UM_TMP/repo1.json"
assert_ok "'./' replaced: source now equals the release source" test "$(jq -c '.plugins[0].source' "$UM_TMP/repo1.json")" = "$(jq -c '.plugins[0].source' "$UM_TMP/rel.json")"
assert_ok "other fields and plugins are preserved" test "$(jq -c 'del(.plugins[0].source)' "$UM_TMP/repo1.json")" = '{"name":"agent-pawl","description":"repo desc","owner":{"name":"Daniel Ferreira"},"plugins":[{"name":"agent-pawl"},{"name":"other","description":"keep me","source":"./other"}]}'
assert_ok "output is jq-pretty-printed with a trailing newline" is_pretty "$UM_TMP/repo1.json"

cp "$UM_TMP/repo1.json" "$UM_TMP/repo1.before"
assert_ok "same version again is a no-op success" run_um "$UM_TMP/rel.json" "$UM_TMP/repo1.json"
assert_ok "same version leaves the file byte-identical" cmp "$UM_TMP/repo1.json" "$UM_TMP/repo1.before"

um_repo "$(archive_src 0.5.0 $SHA_B)" "$UM_TMP/repo2.json"
assert_ok "replaces an older archive source" run_um "$UM_TMP/rel.json" "$UM_TMP/repo2.json"
assert_ok "older archive replaced with 0.6.0" test "$(jq -r '.plugins[0].source.sha256' "$UM_TMP/repo2.json")" = "$SHA_A"

um_release "$UM_TMP/rel_old.json" "$(archive_src 0.5.0 $SHA_B)"
cp "$UM_TMP/repo2.json" "$UM_TMP/repo2.before"
assert_ok "downgrade is refused without failing" run_um "$UM_TMP/rel_old.json" "$UM_TMP/repo2.json"
assert_ok "downgrade leaves the file byte-identical" cmp "$UM_TMP/repo2.json" "$UM_TMP/repo2.before"

um_release "$UM_TMP/rel_rc.json" "$(archive_src 1.0.0-rc1 $SHA_B)"
um_repo "$(archive_src 1.0.0 $SHA_A)" "$UM_TMP/repo3.json"
cp "$UM_TMP/repo3.json" "$UM_TMP/repo3.before"
assert_ok "1.0.0-rc1 over 1.0.0 is a refused downgrade" run_um "$UM_TMP/rel_rc.json" "$UM_TMP/repo3.json"
assert_ok "prerelease downgrade leaves the file byte-identical" cmp "$UM_TMP/repo3.json" "$UM_TMP/repo3.before"

um_release "$UM_TMP/rel_pre.json" "$(archive_src 1.0.0-rc.1 $SHA_B)"
um_repo "$(archive_src 0.6.0 $SHA_A)" "$UM_TMP/repo7.json"
cp "$UM_TMP/repo7.json" "$UM_TMP/repo7.before"
assert_ok "a prerelease source is refused without failing" run_um "$UM_TMP/rel_pre.json" "$UM_TMP/repo7.json"
assert_ok "a prerelease source leaves the file byte-identical" cmp "$UM_TMP/repo7.json" "$UM_TMP/repo7.before"
assert_ok "the prerelease refusal prints a notice" test "$(run_um "$UM_TMP/rel_pre.json" "$UM_TMP/repo7.json")" = "notice: v1.0.0-rc.1 is a prerelease — not pointing $UM_TMP/repo7.json at it"
um_repo '"./"' "$UM_TMP/repo8.json"
cp "$UM_TMP/repo8.json" "$UM_TMP/repo8.before"
assert_ok "a prerelease source is refused even over a './' entry" run_um "$UM_TMP/rel_pre.json" "$UM_TMP/repo8.json"
assert_ok "prerelease over './' leaves the file byte-identical" cmp "$UM_TMP/repo8.json" "$UM_TMP/repo8.before"
um_release "$UM_TMP/rel_final.json" "$(archive_src 1.0.0 $SHA_B)"
assert_ok "a normal newer release still updates after a refused prerelease" run_um "$UM_TMP/rel_final.json" "$UM_TMP/repo7.json"
assert_ok "repo7 now points at 1.0.0" test "$(jq -r '.plugins[0].source.sha256' "$UM_TMP/repo7.json")" = "$SHA_B"

um_release "$UM_TMP/rel_new.json" "$(archive_src 0.10.0 $SHA_B)"
um_repo "$(archive_src 0.9.0 $SHA_A)" "$UM_TMP/repo4.json"
assert_ok "0.10.0 is newer than 0.9.0 (numeric, not lexical)" run_um "$UM_TMP/rel_new.json" "$UM_TMP/repo4.json"
assert_ok "0.9.0 replaced by 0.10.0" test "$(jq -r '.plugins[0].source.sha256' "$UM_TMP/repo4.json")" = "$SHA_B"

um_release "$UM_TMP/bad1.json" '{"source":"archive","url":"https://example.com/agent-pawl-plugin_0.6.0.zip","sha256":"'$SHA_A'"}'
um_repo '"./"' "$UM_TMP/repo5.json"
assert_fails "release file with a bad url is rejected" run_um "$UM_TMP/bad1.json" "$UM_TMP/repo5.json"
um_release "$UM_TMP/bad2.json" '{"source":"archive","url":"https://github.com/dcferreira/agent-pawl/releases/download/v0.6.0/agent-pawl-plugin_0.7.0.zip","sha256":"'$SHA_A'"}'
assert_fails "release url with mismatched tag and zip versions is rejected" run_um "$UM_TMP/bad2.json" "$UM_TMP/repo5.json"
um_release "$UM_TMP/bad3.json" '"./"'
assert_fails "release file with a non-archive source is rejected" run_um "$UM_TMP/bad3.json" "$UM_TMP/repo5.json"
um_release "$UM_TMP/bad4.json" "$(archive_src 0.6.0 DEADBEEF)"
assert_fails "release file with a bad sha256 is rejected" run_um "$UM_TMP/bad4.json" "$UM_TMP/repo5.json"
um_release "$UM_TMP/bad5.json" "$(archive_src 0.6.0 ${SHA_A^^})"
assert_fails "release file with an uppercase sha256 is rejected" run_um "$UM_TMP/bad5.json" "$UM_TMP/repo5.json"
assert_ok "rejected release leaves the repo file untouched" test "$(jq -c '.plugins[0].source' "$UM_TMP/repo5.json")" = '"./"'

jq -n '{name:"agent-pawl",plugins:[{name:"other",source:"./"}]}' >"$UM_TMP/repo6.json"
assert_fails "repo marketplace without an agent-pawl plugin fails" run_um "$UM_TMP/rel.json" "$UM_TMP/repo6.json"
assert_fails "missing repo marketplace file fails" run_um "$UM_TMP/rel.json" "$UM_TMP/nonexistent.json"
assert_fails "no arguments fails" run_um

rm -rf "$UM_TMP"

# --- is-prerelease.sh ---------------------------------------------------
echo
echo "== is-prerelease.sh =="

assert_ok "v1.0.0-rc.1 is a prerelease" is_prerelease v1.0.0-rc.1
assert_ok "1.0.0-rc.10 (no leading v) is a prerelease" is_prerelease 1.0.0-rc.10
assert_fails "v1.0.0 is not a prerelease" is_prerelease v1.0.0
assert_fails "0.7.0 is not a prerelease" is_prerelease 0.7.0
assert_ok "main with a prerelease exits 0" bash -c "PAWL_RELEASE_CHECK_TEST=1; source '$REPO_ROOT/scripts/release/is-prerelease.sh'; main v1.0.0-rc.1"
assert_fails "main with a plain version exits non-zero" bash -c "PAWL_RELEASE_CHECK_TEST=1; source '$REPO_ROOT/scripts/release/is-prerelease.sh'; main v1.0.0"
# Executed (not sourced): the test harness exports PAWL_RELEASE_CHECK_TEST=1,
# which would disable the script's main guard.
assert_ok "executed: prints nothing, exits 0 for a prerelease" bash -c "test -z \"\$(env -u PAWL_RELEASE_CHECK_TEST bash '$REPO_ROOT/scripts/release/is-prerelease.sh' v1.0.0-rc.1)\""
assert_ok "executed: exits 1 for a plain version" test "$(env -u PAWL_RELEASE_CHECK_TEST bash "$REPO_ROOT/scripts/release/is-prerelease.sh" v1.0.0 >/dev/null 2>&1; echo $?)" = 1
assert_ok "executed with no argument exits 2" test "$(env -u PAWL_RELEASE_CHECK_TEST bash "$REPO_ROOT/scripts/release/is-prerelease.sh" >/dev/null 2>&1; echo $?)" = 2

# --- batch.sh ------------------------------------------------------------
echo
echo "== batch.sh =="

# new_batch_fixture [UNRELEASED_COUNT] [RC_COUNT]: a bare dir with just the
# fragment directories batch.sh inspects (it runs from the repo root).
BATCH_DIR=""
new_batch_fixture() {
  BATCH_DIR="$(mktemp -d)"
  mkdir -p "$BATCH_DIR/.changes/unreleased"
  local i
  for ((i = 1; i <= ${1:-0}; i++)); do
    printf 'kind: added\nbody: u%s\n' "$i" >"$BATCH_DIR/.changes/unreleased/added-$i.yaml"
  done
  if [ "${2:-0}" -gt 0 ]; then
    mkdir -p "$BATCH_DIR/.changes/rc"
    for ((i = 1; i <= ${2:-0}; i++)); do
      printf 'kind: added\nbody: r%s\n' "$i" >"$BATCH_DIR/.changes/rc/added-r$i.yaml"
    done
    printf '## v1.0.0-rc.1\n' >"$BATCH_DIR/.changes/v1.0.0-rc.1.md"
  fi
}

# A stub changie that records its argv, so the command line batch.sh builds
# can be asserted without the real tool.
STUB_DIR="$(mktemp -d)"
cat >"$STUB_DIR/changie" <<'EOF'
#!/bin/sh
echo "$*" >>"$CHANGIE_STUB_LOG"
EOF
chmod +x "$STUB_DIR/changie"

# run_batch ARGS... — in $BATCH_DIR, with the stub first on PATH; echoes the
# recorded changie argv (nothing if changie was never invoked).
run_batch() {
  (
    cd "$BATCH_DIR"
    export CHANGIE_STUB_LOG="$BATCH_DIR/changie.log"
    rm -f "$CHANGIE_STUB_LOG"
    PATH="$STUB_DIR:$PATH"
    release_batch "$@" >/dev/null 2>&1 || exit 1
    cat "$CHANGIE_STUB_LOG"
  )
}
batch_log_is() { # EXPECTED ARGS...
  local expected="$1"
  shift
  [ "$(run_batch "$@")" = "$expected" ]
}
batch_refuses() { # ARGS...: fails and never invoked changie
  ! run_batch "$@" >/dev/null && [ ! -e "$BATCH_DIR/changie.log" ]
}

new_batch_fixture 1 0
assert_ok "plain: empty input -> changie batch auto" batch_log_is "batch auto" ""
assert_ok "plain: minor passes through" batch_log_is "batch minor" minor
assert_ok "plain: 1.2.3 is normalised to v1.2.3" batch_log_is "batch v1.2.3" 1.2.3
assert_ok "plain: v1.2.3 passes through" batch_log_is "batch v1.2.3" v1.2.3
assert_ok "prerelease: cut moves fragments to rc" batch_log_is "batch v1.0.0 --prerelease rc.1 --move-dir rc" v1.0.0 rc.1
assert_ok "prerelease: 1.0.0 is normalised to v1.0.0" batch_log_is "batch v1.0.0 --prerelease rc.12 --move-dir rc" 1.0.0 rc.12
assert_ok "prerelease: unreleased rc.9 style numbers accepted" batch_log_is "batch v1.0.0 --prerelease rc.9 --move-dir rc" v1.0.0 rc.9
rm -rf "${BATCH_DIR:?}"

new_batch_fixture 1 0
for bad in rc1 beta.1 rc.0 rc.01 rc. RC.1 rc.1.2 "rc.1 "; do
  assert_ok "bad prerelease '$bad' is refused" batch_refuses v1.0.0 "$bad"
done
assert_ok "prerelease with auto is refused" batch_refuses auto rc.1
assert_ok "prerelease with an empty version (auto) is refused" batch_refuses "" rc.1
assert_ok "prerelease with minor is refused" batch_refuses minor rc.1
assert_ok "prerelease with major is refused" batch_refuses major rc.1
assert_ok "prerelease with patch is refused" batch_refuses patch rc.1
assert_ok "an explicit version with a prerelease suffix is refused" batch_refuses v1.0.0-rc.1 rc.1
rm -rf "${BATCH_DIR:?}"

new_batch_fixture 0 0
assert_ok "plain: empty unreleased aborts" batch_refuses v1.0.0
assert_ok "prerelease: empty unreleased aborts" batch_refuses v1.0.0 rc.1
rm -rf "${BATCH_DIR:?}"

new_batch_fixture 0 2
assert_ok "rc pending: empty unreleased still aborts for a new rc cut" batch_refuses v1.0.0 rc.2
assert_ok "rc pending: auto is refused" batch_refuses auto
assert_ok "rc pending: empty input (auto) is refused" batch_refuses ""
assert_ok "rc pending: minor is refused" batch_refuses minor
assert_ok "rc pending: major is refused" batch_refuses major
assert_ok "rc pending: patch is refused" batch_refuses patch
assert_ok "rc pending: a final cut of a different version is refused" batch_refuses v0.7.1
assert_ok "rc pending: an rc cut of a different base version is refused" batch_refuses v1.1.0 rc.2
assert_ok "rc pending: final cut with an empty unreleased dir is allowed" batch_log_is "batch v1.0.0 --include rc --remove-prereleases" v1.0.0
assert_ok "rc pending: 1.0.0 is normalised for the final cut" batch_log_is "batch v1.0.0 --include rc --remove-prereleases" 1.0.0
rm -rf "${BATCH_DIR:?}"

new_batch_fixture 1 1
assert_ok "rc pending + new fragments: the next rc cut is allowed" batch_log_is "batch v1.0.0 --prerelease rc.2 --move-dir rc" v1.0.0 rc.2
assert_ok "rc pending + new fragments: final cut includes the rc" batch_log_is "batch v1.0.0 --include rc --remove-prereleases" v1.0.0
rm -rf "${BATCH_DIR:?}"
rm -rf "${STUB_DIR:?}"

# --- changie availability gate (mirrors PAWL_REQUIRE_VCS_TOOLS) ----------
PINNED_CHANGIE=1.26.0
changie_gate() {
  if ! command -v changie >/dev/null 2>&1; then
    if [ "${PAWL_REQUIRE_CHANGIE:-0}" = "1" ]; then
      tests_run=$((tests_run + 1))
      echo "FAIL: changie is not on PATH and PAWL_REQUIRE_CHANGIE=1 (install github.com/miniscruff/changie@v$PINNED_CHANGIE)"
      failures=$((failures + 1))
    else
      echo "skip: changie is not on PATH — release-candidate end-to-end proof skipped (set PAWL_REQUIRE_CHANGIE=1 to make this a failure)"
    fi
    return 1
  fi
  # A release binary reports its version via --version, but a `go install
  # ...@v1.26.0` build (what CI and release-pr.yml use) is not ldflag-stamped
  # and prints "vdev"; its module version is in the embedded build info.
  local got
  got="$(changie --version 2>&1 | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || true)"
  if [ -z "$got" ] && command -v go >/dev/null 2>&1; then
    got="$(go version -m "$(command -v changie)" 2>/dev/null |
      awk '$1 == "mod" && $2 == "github.com/miniscruff/changie" { sub(/^v/, "", $3); print $3 }' || true)"
  fi
  if [ "$got" != "$PINNED_CHANGIE" ]; then
    tests_run=$((tests_run + 1))
    echo "FAIL: changie on PATH is '${got:-unknown}', the release pipeline pins $PINNED_CHANGIE (the rc behaviour was verified against it)"
    failures=$((failures + 1))
    return 1
  fi
  return 0
}

# --- end-to-end release-candidate proof (real changie) -------------------
#
# 2 fragments -> rc.1 -> 1 fragment -> rc.2 -> final with nothing new. The
# fixture copies the repo's real changie config, header and plugin.json so a
# change to any of them (kinds, replacements) is exercised here.
if changie_gate; then
  echo
  echo "== batch.sh end-to-end (changie $PINNED_CHANGIE) =="

  E2E="$(mktemp -d)"
  mkdir -p "$E2E/.changes/unreleased" "$E2E/.claude-plugin"
  cp "$REPO_ROOT/.changie.yaml" "$E2E/"
  cp "$REPO_ROOT/.changes/header.tpl.md" "$E2E/.changes/"
  cp "$REPO_ROOT/.claude-plugin/plugin.json" "$E2E/.claude-plugin/"
  sed -i 's/^  "version": ".*",/  "version": "0.7.0",/' "$E2E/.claude-plugin/plugin.json"
  printf '## v0.7.0 - 2026-01-01\n\n### Added\n\n* Seed.\n' >"$E2E/.changes/v0.7.0.md"
  jq -n --arg s "$SHA_A" '{name:"agent-pawl",plugins:[{name:"agent-pawl",source:{source:"archive",url:"https://github.com/dcferreira/agent-pawl/releases/download/v0.7.0/agent-pawl-plugin_0.7.0.zip",sha256:$s}}]}' >"$E2E/.claude-plugin/marketplace.json"

  e2e_fragment() { # NAME KIND BODY
    printf 'kind: %s\nbody: %s\ntime: 2026-10-14T10:00:%s.000000+00:00\ncustom:\n  PR: "1"\n' "$2" "$3" "$(printf '%02d' $((++E2E_SEQ)))" >"$E2E/.changes/unreleased/$1.yaml"
  }
  E2E_SEQ=0
  e2e_cut() { # ARGS... : batch + merge, in the fixture
    (cd "$E2E" && release_batch "$@" && changie merge)
  }
  e2e_latest() { (cd "$E2E" && changie latest); }
  e2e_plugin_version() { jq -r .version "$E2E/.claude-plugin/plugin.json"; }
  e2e_state_is() { # LATEST PLUGIN_VERSION
    [ "$(e2e_latest)" = "$1" ] && [ "$(e2e_plugin_version)" = "$2" ]
  }

  # rc.1
  e2e_fragment a1 added "feat A"
  e2e_fragment a2 fixed "fix B"
  assert_ok "rc.1 cut succeeds" e2e_cut v1.0.0 rc.1
  assert_ok "rc.1: changie latest is v1.0.0-rc.1, plugin.json is 1.0.0-rc.1" e2e_state_is v1.0.0-rc.1 1.0.0-rc.1
  assert_ok "rc.1: version consistency passes" check_version_consistency "$E2E"
  assert_ok "rc.1: fragments parked in .changes/rc" test "$(ls "$E2E/.changes/rc" | wc -l)" = 2
  assert_ok "rc.1: unreleased is empty" test -z "$(ls "$E2E/.changes/unreleased"/*.yaml 2>/dev/null)"
  assert_ok "rc.1 notes hold both fragments" bash -c "grep -q 'feat A' '$E2E/.changes/v1.0.0-rc.1.md' && grep -q 'fix B' '$E2E/.changes/v1.0.0-rc.1.md'"

  # rc.2
  e2e_fragment a3 added "feat C"
  assert_ok "rc.2 cut succeeds" e2e_cut v1.0.0 rc.2
  assert_ok "rc.2: changie latest is v1.0.0-rc.2, plugin.json is 1.0.0-rc.2" e2e_state_is v1.0.0-rc.2 1.0.0-rc.2
  assert_ok "rc.2: version consistency passes" check_version_consistency "$E2E"
  assert_ok "rc.2 notes contain only their own delta" bash -c "grep -q 'feat C' '$E2E/.changes/v1.0.0-rc.2.md' && ! grep -q 'feat A' '$E2E/.changes/v1.0.0-rc.2.md' && ! grep -q 'fix B' '$E2E/.changes/v1.0.0-rc.2.md'"
  assert_ok "rc.2: all three fragments are parked in .changes/rc" test "$(ls "$E2E/.changes/rc" | wc -l)" = 3
  assert_fails "rc pending: auto is refused against the real tree" bash -c "cd '$E2E' && PAWL_RELEASE_CHECK_TEST=1 && source '$REPO_ROOT/scripts/release/batch.sh' && release_batch auto"

  # final
  assert_ok "final cut (nothing new) succeeds" e2e_cut v1.0.0
  assert_ok "final: changie latest is v1.0.0, plugin.json is 1.0.0" e2e_state_is v1.0.0 1.0.0
  assert_ok "final: version consistency passes" check_version_consistency "$E2E"
  assert_ok "final: .changes/rc is gone" test ! -e "$E2E/.changes/rc"
  assert_ok "final: no v1.0.0-rc.*.md left" test -z "$(ls "$E2E/.changes"/v1.0.0-rc.*.md 2>/dev/null)"
  assert_ok "final: v1.0.0.md holds all three bodies" bash -c "for b in 'feat A' 'fix B' 'feat C'; do grep -q \"\$b\" '$E2E/.changes/v1.0.0.md' || exit 1; done"
  assert_ok "final: CHANGELOG.md has all three bodies" bash -c "for b in 'feat A' 'fix B' 'feat C'; do grep -q \"\$b\" '$E2E/CHANGELOG.md' || exit 1; done"
  assert_ok "final: CHANGELOG.md has exactly one '## v1.0.0' heading" test "$(grep -c '^## v1.0.0 ' "$E2E/CHANGELOG.md")" = 1
  assert_ok "final: CHANGELOG.md has no rc heading" test "$(grep -c '^## .*rc' "$E2E/CHANGELOG.md")" = 0
  rm -rf "${E2E:?}"
fi

echo
echo "$tests_run tests run, $failures failed"
if [ "$failures" -ne 0 ]; then
  exit 1
fi
