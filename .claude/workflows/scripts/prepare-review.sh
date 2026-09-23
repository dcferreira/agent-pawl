#!/usr/bin/env sh
# prepare-review.sh <vcs> <pr_number> <repo> <base_branch> <reviewer_choices>
#
# Body of the `prepare_review` deterministic step, the entry point of every
# review round. It makes the round's inputs hard rather than best-effort:
#
# 1. The local working copy must be clean (check-clean.sh). fix-push.sh
#    later commits *everything* in it as the round's fix, so unrelated local
#    edits would otherwise ride along into the PR. (The same check runs
#    again after the reviewers, in `reviewers_left_tree_clean`, for files
#    the reviewers themselves left behind.)
# 2. The local head must BE the PR's head (headRefOid). Otherwise codex and
#    fix_issues would work on a different tree than the diff the other
#    reviewers see, and fix-push.sh would push unrelated history onto the PR
#    branch. "Local head" is `HEAD` under git and `@-` under jj (the
#    workflow's convention: @ is the empty working-copy commit on top of the
#    PR head — `jj bookmark track <branch>@origin && jj new <branch>` gets
#    you there; fix-push.sh also tracks it). Right after a push GitHub can
#    briefly still report the previous head, so a mismatch is re-checked a
#    few times before failing.
# 3. The PR diff is fetched with `gh pr diff` into a file and its path is
#    written to state. The reviewers read it as a plain-file context: entry,
#    which — unlike a `!cmd` entry, whose failure silently degrades to empty
#    output — fails the branch loudly if the file is missing. A gh error or
#    an empty diff fails this step, so a round can never "review" nothing
#    and conclude the PR is clean. The file lives under the user cache dir,
#    outside the working copy (so fix-push.sh never commits it) and outside
#    /tmp (so it survives a reboot and a resumed run can still read it).
# 4. `codex_base`, the ref ai_review_codex passes to `codex review --base`:
#    only when "codex" is in <reviewer_choices> (otherwise empty, and
#    nothing is fetched), `origin/<base_branch>`, freshly fetched (`git fetch` under git, `jj git
#    fetch` under a colocated jj repo, which writes the same git
#    remote-tracking ref) so codex never diffs against a stale local
#    `main`. Also empty when this directory is not a git work tree of its
#    own (is-git-worktree.sh; e.g. a non-colocated jj workspace): codex review cannot run there at all,
#    and check_reviewers has already refused the codex option in that case.
#
# Prints {"head_sha": ..., "diff_file": ..., "codex_base": ...,
# "ci_round": false, "fix_note": ""} on one line (emits: json). `ci_round: false` marks the round that follows
# as a review round (a fresh round always starts here), so unchanged_route
# can tell it apart from a ci_failure-originated one later. `fix_note` is
# reset to "" here so a prior round's ask_nitpicks instructions never leak
# into a new round.
# PAWL_REVIEW_HEAD_TRIES / PAWL_REVIEW_HEAD_SLEEP tune the head re-check
# (defaults 10 tries, 3s apart); tests set them low.
set -eu

vcs="${1:?prepare-review.sh: vcs argument required}"
pr_number="${2:?prepare-review.sh: pr_number argument required}"
repo="${3:?prepare-review.sh: repo argument required}"
base_branch="${4:?prepare-review.sh: base_branch argument required}"
reviewer_choices="${5:?prepare-review.sh: reviewer_choices argument required}"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tries_max="${PAWL_REVIEW_HEAD_TRIES:-10}"
sleep_s="${PAWL_REVIEW_HEAD_SLEEP:-3}"

# fetch-pr.sh resolved this once from origin's URL; every gh call here uses
# it instead of letting gh infer a repo from cwd, which fails outright in a
# non-colocated jj workspace (no .git directory at all).
export GH_REPO="$repo"

"$script_dir/check-clean.sh" "$vcs" "prepare-review.sh: the working copy has uncommitted changes; the fix step
commits everything in it, so start from a clean checkout of the PR head:"

case "$vcs" in
  jj) local_head=$(jj log --no-graph -r @- -T commit_id) ;;
  git) local_head=$(git rev-parse HEAD) ;;
esac

tries=0
while :; do
  # Not a pipeline: without pipefail a gh failure would surface only as a
  # misleading head mismatch. Fail on the real cause instead.
  if ! view=$(gh pr view "$pr_number" --json headRefOid); then
    echo "prepare-review.sh: gh pr view ${pr_number} failed" >&2
    exit 1
  fi
  if ! pr_head=$(printf '%s' "$view" | jq -er '.headRefOid'); then
    echo "prepare-review.sh: gh pr view ${pr_number} returned no headRefOid: ${view}" >&2
    exit 1
  fi
  if [ "$pr_head" = "$local_head" ]; then
    break
  fi
  tries=$((tries + 1))
  if [ "$tries" -ge "$tries_max" ]; then
    {
      echo "prepare-review.sh: local head ${local_head} is not PR #${pr_number}'s head ${pr_head}."
      echo "Check out the PR head first (git: gh pr checkout ${pr_number}; jj: jj bookmark track <pr-branch>@origin && jj new <pr-branch>)."
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

codex_base=""
wants_codex=$(printf '%s' "$reviewer_choices" | jq -r 'if type == "array" then (index("codex") != null) else false end')
if [ "$wants_codex" = "true" ] && "$script_dir/is-git-worktree.sh"; then
  case "$vcs" in
    jj) jj git fetch --remote origin --branch "exact:\"${base_branch}\"" >&2 ;;
    git) git fetch --quiet origin "+refs/heads/${base_branch}:refs/remotes/origin/${base_branch}" >&2 ;;
  esac
  codex_base="origin/${base_branch}"
fi

jq -cn --arg head_sha "$pr_head" --arg diff_file "$diff_file" --arg codex_base "$codex_base" \
  '{head_sha: $head_sha, diff_file: $diff_file, codex_base: $codex_base, ci_round: false, fix_note: ""}'
