#!/usr/bin/env bash
# Fails a PR that doesn't add a changie fragment (.changes/unreleased/*.yaml),
# unless it's exempt, and fails if ANY .changes/unreleased/*.yaml fragment at
# HEAD has a missing or unknown `kind:`. changie matches `kind:` against the
# kind KEY in .changie.yaml (`added`), not its label (`Added`); a bad kind
# slips past the "fragment added" check but breaks `changie batch` at release
# time, so the kind check runs on every fragment present at HEAD (not just
# the ones this PR adds) and is NOT waived by the `skip changelog` label.
# Only the bootstrap and release/v* exemptions skip it. See docs/releasing.md
# for the fragment workflow this enforces.
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

# changie_kind_keys REV
# Prints the `key:` of every entry in the top-level `kinds:` list of
# REV:.changie.yaml, one per line. Plain awk: only `key:` lines indented
# inside the `kinds:` block count (the block ends at the next top-level key),
# so a `key:` under `custom:` is ignored. Surrounding quotes are stripped.
changie_kind_keys() {
  local rev="$1"
  git show "${rev}:.changie.yaml" | awk '
    /^kinds:[[:space:]]*$/ { in_kinds = 1; next }
    /^[^[:space:]#-]/ { in_kinds = 0 }
    in_kinds && /^[[:space:]]*-?[[:space:]]*key:/ {
      v = $0
      sub(/^[^:]*:[[:space:]]*/, "", v)
      sub(/[[:space:]]*(#.*)?$/, "", v)
      gsub(/^["\x27]|["\x27]$/, "", v)
      print v
    }'
}

# fragment_kind REV PATH
# Prints the `kind:` value of REV:PATH with optional quotes stripped; prints
# nothing if there is no `kind:` line.
fragment_kind() {
  local rev="$1" path="$2"
  git show "${rev}:${path}" | awk '
    /^kind:/ {
      v = $0
      sub(/^kind:[[:space:]]*/, "", v)
      sub(/[[:space:]]*(#.*)?$/, "", v)
      gsub(/^["\x27]|["\x27]$/, "", v)
      print v
      exit
    }'
}

# invalid_fragment_kinds REV
# For every .changes/unreleased/*.yaml at REV, prints one FAIL line to stdout
# per file whose kind is missing or not a key in REV's .changie.yaml.
# Prints nothing if all are valid.
invalid_fragment_kinds() {
  local rev="$1"
  local keys valid path kind
  keys=$(changie_kind_keys "$rev")
  valid=$(echo "$keys" | paste -sd, - | sed 's/,/, /g')
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    kind=$(fragment_kind "$rev" "$path")
    if [ -z "$kind" ]; then
      echo "FAIL: $path: no 'kind:' line (valid: $valid)"
    elif ! echo "$keys" | grep -qxF -- "$kind"; then
      echo "FAIL: $path: kind '$kind' is not a kind key in .changie.yaml (valid: $valid)"
    fi
  done < <(git ls-tree --name-only -r "$rev" -- .changes/unreleased/ | grep '\.yaml$' || true)
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

  local bad
  bad=$(invalid_fragment_kinds "$head")
  if [ -n "$bad" ]; then
    echo "$bad" >&2
    echo "FAIL: fragment kinds must be the lowercase kind key from .changie.yaml (changie new writes it for you)." >&2
    return 1
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
