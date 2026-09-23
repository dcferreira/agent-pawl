#!/usr/bin/env sh
# prepare-review.sh <vcs> <pr_number>
#
# Body of the `prepare_review` deterministic step, the entry point of every
# review round. It makes the round's inputs hard rather than best-effort:
#
# 1. The local working copy must be clean. fix-push.sh later commits
#    *everything* in it as the round's fix, so unrelated local edits would
#    otherwise ride along into the PR.
# 2. The local head must BE the PR's head (headRefOid). Otherwise codex and
#    fix_issues would work on a different tree than the diff the other
#    reviewers see, and fix-push.sh would push unrelated history onto the PR
#    branch. "Local head" is `HEAD` under git and `@-` under jj (the
#    workflow's convention: @ is the empty working-copy commit on top of the
#    PR head — `jj new <branch>` gets you there). Right after a push GitHub
#    can briefly still report the previous head, so a mismatch is re-checked
#    a few times before failing.
# 3. The PR diff is fetched with `gh pr diff` into a file and its path is
#    written to state. The reviewers read it as a plain-file context: entry,
#    which — unlike a `!cmd` entry, whose failure silently degrades to empty
#    output — fails the branch loudly if the file is missing. A gh error or
#    an empty diff fails this step, so a round can never "review" nothing
#    and conclude the PR is clean. The file lives under the user cache dir,
#    outside the working copy (so fix-push.sh never commits it) and outside
#    /tmp (so it survives a reboot and a resumed run can still read it).
#
# Prints {"head_sha": ..., "diff_file": ...} on one line (emits: json).
# PAWL_REVIEW_HEAD_TRIES / PAWL_REVIEW_HEAD_SLEEP tune the head re-check
# (defaults 10 tries, 3s apart); tests set them low.
set -eu

vcs="${1:?prepare-review.sh: vcs argument required}"
pr_number="${2:?prepare-review.sh: pr_number argument required}"
tries_max="${PAWL_REVIEW_HEAD_TRIES:-10}"
sleep_s="${PAWL_REVIEW_HEAD_SLEEP:-3}"

case "$vcs" in
  jj)
    dirty=$(jj diff -r @ --summary)
    local_head=$(jj log --no-graph -r @- -T commit_id)
    ;;
  git)
    dirty=$(git status --porcelain)
    local_head=$(git rev-parse HEAD)
    ;;
  *)
    echo "prepare-review.sh: unknown vcs '${vcs}' (expected jj or git)" >&2
    exit 1
    ;;
esac

if [ -n "$dirty" ]; then
  {
    echo "prepare-review.sh: the working copy has uncommitted changes; the fix step"
    echo "commits everything in it, so start from a clean checkout of the PR head:"
    echo "$dirty"
  } >&2
  exit 1
fi

tries=0
while :; do
  pr_head=$(gh pr view "$pr_number" --json headRefOid | jq -r '.headRefOid')
  if [ "$pr_head" = "$local_head" ]; then
    break
  fi
  tries=$((tries + 1))
  if [ "$tries" -ge "$tries_max" ]; then
    {
      echo "prepare-review.sh: local head ${local_head} is not PR #${pr_number}'s head ${pr_head}."
      echo "Check out the PR head first (git: gh pr checkout ${pr_number}; jj: jj new <pr-branch>)."
    } >&2
    exit 1
  fi
  sleep "$sleep_s"
done

cache_dir="${XDG_CACHE_HOME:-${HOME}/.cache}/pawl-review-pr"
mkdir -p "$cache_dir"
diff_file="${cache_dir}/pr-${pr_number}-${pr_head}.diff"

if ! gh pr diff "$pr_number" >"${diff_file}.tmp"; then
  rm -f "${diff_file}.tmp"
  echo "prepare-review.sh: gh pr diff ${pr_number} failed" >&2
  exit 1
fi
if [ ! -s "${diff_file}.tmp" ]; then
  rm -f "${diff_file}.tmp"
  echo "prepare-review.sh: gh pr diff ${pr_number} returned an empty diff — nothing to review" >&2
  exit 1
fi
mv "${diff_file}.tmp" "$diff_file"

jq -cn --arg head_sha "$pr_head" --arg diff_file "$diff_file" \
  '{head_sha: $head_sha, diff_file: $diff_file}'
