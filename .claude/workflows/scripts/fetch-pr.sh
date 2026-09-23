#!/usr/bin/env sh
# fetch-pr.sh <pr_number>
#
# Body of the `fetch_pr` deterministic step. Prints a single JSON object
# (emits: json, the default) remapping `gh pr view`'s field names onto this
# workflow's state keys, plus this checkout's VCS (jj or git, from
# detect-vcs.sh) and the GitHub repo (owner/repo) — deliberately one JSON
# object rather than `emits: pairs`, since a PR title routinely contains
# spaces and the pairs grammar (whitespace-separated k=v tokens) cannot
# carry that safely. `jq -c` is load-bearing: the engine parses only the
# last non-empty stdout line (format-spec §B.1), and pretty-printed JSON's
# last line is just `}`.
#
# Every step in this workflow runs with cwd at the checkout root, which
# under a non-colocated jj workspace has no `.git` directory at all — `gh`
# cannot infer a repo from cwd there, and even under git it may pick the
# wrong remote (`upstream`, or whatever `gh repo set-default` chose) rather
# than the one fix-push.sh actually pushes to (`origin`). So this step
# resolves owner/repo itself, once, from `origin`'s remote URL — the base
# repo IS derived from origin, by construction it always agrees with where
# fix-push.sh pushes — and every other gh-using script takes it as an
# argument instead of letting gh guess.
set -eu

pr_number="${1:?fetch-pr.sh: pr_number argument required}"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

vcs=$("$script_dir/detect-vcs.sh")

# Parses owner/repo out of a github.com remote URL in any of the forms:
#   git@github.com:owner/repo(.git)
#   https://github.com/owner/repo(.git)
#   ssh://git@github.com/owner/repo(.git)
# with an optional trailing slash. Prints "owner/repo" and exits 0, or
# prints nothing and exits 1 for anything else (a non-GitHub host, or a
# path that isn't exactly two segments).
parse_github_repo() {
  url="$1"
  case "$url" in
    git@github.com:*) rest="${url#git@github.com:}" ;;
    https://github.com/*) rest="${url#https://github.com/}" ;;
    ssh://git@github.com/*) rest="${url#ssh://git@github.com/}" ;;
    *) return 1 ;;
  esac
  rest="${rest%/}"
  rest="${rest%.git}"
  case "$rest" in
    */*/*|"") return 1 ;;
    */*) printf '%s\n' "$rest" ;;
    *) return 1 ;;
  esac
}

case "$vcs" in
  jj)
    origin_url=$(jj git remote list 2>/dev/null | awk '$1 == "origin" { print $2; exit }')
    ;;
  git)
    origin_url=$(git remote get-url origin 2>/dev/null) || origin_url=""
    ;;
  *)
    echo "fetch-pr.sh: unknown vcs '${vcs}' (expected jj or git)" >&2
    exit 1
    ;;
esac

if [ -z "${origin_url:-}" ]; then
  echo "fetch-pr.sh: no 'origin' remote found (vcs=${vcs}) — this workflow resolves the GitHub repo from origin's URL" >&2
  exit 1
fi

if ! repo=$(parse_github_repo "$origin_url"); then
  echo "fetch-pr.sh: origin's URL '${origin_url}' is not a github.com URL I can parse (expected git@github.com:owner/repo, https://github.com/owner/repo, or ssh://git@github.com/owner/repo)" >&2
  exit 1
fi

# Capture gh's output before parsing it: in a pipeline (no pipefail under
# sh) a gh auth/network error would be masked by jq's exit status, and the
# step would exit 0 with no output.
if ! view=$(gh pr view "$pr_number" -R "$repo" \
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

printf '%s' "$view" | jq -c --arg vcs "$vcs" --arg repo "$repo" '{
    vcs: $vcs,
    repo: $repo,
    pr_url: .url,
    head_sha: .headRefOid,
    base_branch: .baseRefName,
    branch: .headRefName,
    title: .title
  }'
