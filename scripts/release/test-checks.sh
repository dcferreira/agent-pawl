#!/usr/bin/env bash
# Unit tests for scripts/release/check-fragment.sh,
# check-no-version-bump.sh and check-version-consistency.sh — run with
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
assert_ok "prerelease file (v1.0.0-rc1.md) ignored, v1.0.0 is latest -> pass" check_version_consistency "$FIXTURE_DIR"

echo
echo "$tests_run tests run, $failures failed"
if [ "$failures" -ne 0 ]; then
  exit 1
fi
