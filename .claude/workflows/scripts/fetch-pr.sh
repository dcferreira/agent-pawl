#!/usr/bin/env sh
# fetch-pr.sh <pr_number>
#
# Body of the `fetch_pr` deterministic step. Prints a single JSON object
# (emits: json, the default) remapping `gh pr view`'s field names onto this
# workflow's state keys, plus this checkout's VCS (jj or git, from
# detect-vcs.sh) — deliberately one JSON object rather than `emits: pairs`,
# since a PR title routinely contains spaces and the pairs grammar
# (whitespace-separated k=v tokens) cannot carry that safely. `jq -c` is
# load-bearing: the engine parses only the last non-empty stdout line
# (format-spec §B.1), and pretty-printed JSON's last line is just `}`.
set -eu

pr_number="${1:?fetch-pr.sh: pr_number argument required}"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

vcs=$("$script_dir/detect-vcs.sh")

# Capture gh's output before parsing it: in a pipeline (no pipefail under
# sh) a gh auth/network error would be masked by jq's exit status, and the
# step would exit 0 with no output.
if ! view=$(gh pr view "$pr_number" \
  --json url,headRefOid,baseRefName,headRefName,title,isCrossRepository); then
  echo "fetch-pr.sh: gh pr view ${pr_number} failed" >&2
  exit 1
fi

# Cross-repository (fork) PRs are refused outright. fix-push.sh pushes to
# `origin` (the base repo) under headRefName; for a fork PR that is the wrong
# repository — and a fork's PR from its own `main` would push the fork's
# commits straight onto the base repo's `main`. Anything but an explicit
# `false` (including a missing field) is treated as cross-repo.
cross=$(printf '%s' "$view" | jq -r '.isCrossRepository')
if [ "$cross" != "false" ]; then
  echo "fetch-pr.sh: PR #${pr_number} is a cross-repository (fork) PR (isCrossRepository=${cross}); this workflow only pushes fixes to same-repository PR branches" >&2
  exit 1
fi
head_ref=$(printf '%s' "$view" | jq -er '.headRefName')
base_ref=$(printf '%s' "$view" | jq -er '.baseRefName')
if [ "$head_ref" = "$base_ref" ]; then
  echo "fetch-pr.sh: PR #${pr_number}'s head branch is its base branch (${base_ref}); refusing to push fixes to it" >&2
  exit 1
fi

printf '%s' "$view" | jq -c --arg vcs "$vcs" '{
    vcs: $vcs,
    pr_url: .url,
    head_sha: .headRefOid,
    base_branch: .baseRefName,
    branch: .headRefName,
    title: .title
  }'
