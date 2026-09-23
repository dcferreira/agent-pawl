#!/usr/bin/env sh
# fix-push.sh <vcs> <branch> <round>
#
# Body of the `fix_push` deterministic step: commits whatever fix_issues
# left in the working copy as "review round N" and pushes it to the PR's
# branch, under either VCS this repo (or any repo this workflow runs
# against) might use. prepare-review.sh has already checked, at the start
# of this round, that the working copy was clean and sat on the PR head.
#
# No changes: if fix_issues left nothing to commit (e.g. it judged every
# finding a false positive), nothing is committed or pushed under either VCS
# and the step prints `unchanged`, which the workflow routes to `blocked` so
# a human looks at why — re-reviewing an unchanged diff would just raise the
# same findings again.
#
# Fast-forward only: the new commit must descend from the PR branch's
# current remote head, or the step fails without pushing. This is what keeps
# a run from rewriting the PR branch with unrelated history.
#
# jj (including a colocated jj+git repo, verified against a scratch repo
# while designing this): `jj commit` finalizes the working-copy changes into
# a new commit and moves @ to a fresh empty one on top of it — the finished
# commit is then @-. Bookmarks do NOT follow @ automatically past the first
# commit (verified: a second round's push without an explicit `bookmark
# set` left the remote bookmark stuck on round 1), so `branch` is
# explicitly re-pointed at @- every round before pushing — without
# --allow-backwards, so jj itself also refuses a backwards/sideways move.
#
# git: the familiar add/commit/push, pushing HEAD to the named branch
# explicitly rather than relying on the checkout already tracking it; a
# plain (non-force) push refuses a non-fast-forward on its own too.
#
# Prints `pushed round=<n+1>` or `unchanged` on its last line (emits: pairs;
# an integer has no whitespace to break that grammar).
set -eu

vcs="${1:?fix-push.sh: vcs argument required}"
branch="${2:?fix-push.sh: branch argument required}"
round="${3:?fix-push.sh: round argument required}"

next_round=$((round + 1))
msg="review round ${next_round}"

not_ff() {
  echo "fix-push.sh: the local change does not descend from origin's ${branch} — refusing to push (it would rewrite the PR branch)" >&2
  exit 1
}

case "$vcs" in
  jj)
    if [ -z "$(jj diff -r @ --summary)" ]; then
      echo unchanged
      exit 0
    fi
    remote_rev="\"${branch}\"@origin"
    if [ -z "$(jj log --no-graph -r "(${remote_rev}) & ::@" -T commit_id)" ]; then
      not_ff
    fi
    jj commit -m "$msg"
    jj bookmark set "$branch" -r @-
    jj git push --bookmark "$branch"
    ;;
  git)
    git add -A
    if git diff --cached --quiet; then
      echo unchanged
      exit 0
    fi
    git fetch --quiet origin "refs/heads/${branch}"
    git merge-base --is-ancestor FETCH_HEAD HEAD || not_ff
    git commit -m "$msg"
    git push origin "HEAD:refs/heads/${branch}"
    ;;
  *)
    echo "fix-push.sh: unknown vcs '${vcs}' (expected jj or git)" >&2
    exit 1
    ;;
esac

echo "pushed round=${next_round}"
