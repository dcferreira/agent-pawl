#!/usr/bin/env sh
# fix-push.sh <vcs> <branch> <round>
#
# Body of the `fix_push` deterministic step: commits whatever fix_issues
# left in the working copy as "review round N" and pushes it to the PR's
# branch, under either VCS this repo (or any repo this workflow runs
# against) might use.
#
# jj (including a colocated jj+git repo, verified against a scratch repo
# while designing this): `jj commit` finalizes the working-copy changes into
# a new commit and moves @ to a fresh empty one on top of it — the finished
# commit is then @-. Bookmarks do NOT follow @ automatically past the first
# commit (verified: a second round's push without an explicit `bookmark
# set` left the remote bookmark stuck on round 1), so `branch` is
# explicitly re-pointed at @- every round before pushing.
#
# git: the familiar add/commit/push, pushing HEAD to the named branch
# explicitly rather than relying on the checkout already tracking it.
#
# Prints `round=<n+1>` on its last line (emits: pairs, an integer has no
# whitespace to break that grammar).
set -eu

vcs="${1:?fix-push.sh: vcs argument required}"
branch="${2:?fix-push.sh: branch argument required}"
round="${3:?fix-push.sh: round argument required}"

next_round=$((round + 1))
msg="review round ${next_round}"

case "$vcs" in
  jj)
    jj commit -m "$msg"
    jj bookmark set "$branch" -r @- --allow-backwards
    jj git push --bookmark "$branch"
    ;;
  git)
    git add -A
    git commit -m "$msg"
    git push origin "HEAD:${branch}"
    ;;
  *)
    echo "fix-push.sh: unknown vcs '${vcs}' (expected jj or git)" >&2
    exit 1
    ;;
esac

echo "round=${next_round}"
