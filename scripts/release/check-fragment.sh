#!/usr/bin/env bash
# Fails a PR that doesn't add a changie fragment (.changes/unreleased/*.yaml),
# unless it's exempt. See docs/releasing.md for the fragment workflow this
# enforces.
#
# Usage: check-fragment.sh <base-sha> <head-sha>
# Env:
#   PR_LABELS    - the PR's labels, newline- or comma-separated. A "skip
#                  changelog" label exempts the PR.
#   PR_HEAD_REF  - the PR's head branch name. A release/v* branch (opened by
#                  release-pr.yml) is exempt: it consumes fragments, it
#                  doesn't add one. This is a convenience, not a security
#                  boundary — it's keyed on the branch name alone, and
#                  anyone who can push a branch can already edit this
#                  workflow.
#
# Sourced (not executed) by test-checks.sh, so its logic is broken into
# small functions — mirrors scripts/test-install.sh's approach for
# install.sh. The guard at the bottom (PAWL_RELEASE_CHECK_TEST) keeps the
# real check from running just because the file was sourced.
#
# Deliberately operates on the CALLER's current working directory's git
# repo (a plain `git diff`/`git cat-file`), not on this script's own repo —
# that's what lets test-checks.sh point it at a throwaway fixture repo built
# in a temp dir, and it's also what CI wants (a workflow step's cwd is
# already the checked-out repo root).
set -euo pipefail

# pr_labels_has_skip LABELS
# LABELS is newline- or comma-separated (both forms show up depending on how
# the caller joins github.event.pull_request.labels.*.name).
pr_labels_has_skip() {
  local labels="$1"
  echo "$labels" | tr ',' '\n' | grep -qx 'skip changelog'
}

# pr_head_is_release_branch REF
pr_head_is_release_branch() {
  local ref="$1"
  case "$ref" in
    release/v*) return 0 ;;
    *) return 1 ;;
  esac
}

# changie_config_exists_at REV
changie_config_exists_at() {
  local rev="$1"
  git cat-file -e "${rev}:.changie.yaml" 2>/dev/null
}

# diff_adds_fragment BASE HEAD
# True if the PR branch itself (merge-base...head, three-dot) adds at least
# one .changes/unreleased/*.yaml file. Three-dot rather than two-dot: a
# release merged into base after the PR branched deletes fragments on base,
# which a two-dot base-vs-head diff would otherwise report as "added" by
# this PR (absent on base, present on head) even though the PR added none
# of its own.
diff_adds_fragment() {
  local base="$1" head="$2"
  local added
  added=$(git diff --name-only --diff-filter=A "$base...$head" -- '.changes/unreleased/*.yaml')
  [ -n "$added" ]
}

check_fragment() {
  local base="$1" head="$2"
  local labels="${PR_LABELS:-}"
  local head_ref="${PR_HEAD_REF:-}"

  if ! changie_config_exists_at "$base"; then
    echo "ok: bootstrap (.changie.yaml doesn't exist at base) — this PR introduces the changelog system"
    return 0
  fi

  if pr_head_is_release_branch "$head_ref"; then
    echo "ok: release branch ($head_ref) consumes fragments, doesn't add one"
    return 0
  fi

  if pr_labels_has_skip "$labels"; then
    echo "ok: 'skip changelog' label present"
    return 0
  fi

  if diff_adds_fragment "$base" "$head"; then
    echo "ok: PR adds a .changes/unreleased/*.yaml fragment"
    return 0
  fi

  echo "FAIL: no .changes/unreleased/*.yaml fragment added by this PR." \
    "Run 'changie new' to add one, or apply the 'skip changelog' label if this change has no user-visible effect." >&2
  return 1
}

main() {
  if [ "$#" -ne 2 ]; then
    echo "usage: check-fragment.sh <base-sha> <head-sha>" >&2
    exit 1
  fi
  check_fragment "$1" "$2"
}

if [ "${PAWL_RELEASE_CHECK_TEST:-0}" != "1" ]; then
  main "$@"
fi
