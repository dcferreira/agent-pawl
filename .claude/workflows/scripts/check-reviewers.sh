#!/usr/bin/env sh
# check-reviewers.sh <reviewer_choices>
#
# Body of the `check_reviewers` deterministic step, right after
# choose_reviewers. Prints `ok`, or `codex_needs_git` when "codex" was
# picked but this working copy is not a git work tree of its own — e.g. a
# non-colocated jj workspace, which has a .jj directory and no .git.
# `codex review --base <ref>` diffs against a git ref, so it cannot run
# there; the workflow routes `codex_needs_git` back to choose_reviewers
# (pick again without codex) instead of letting the codex branch fail the
# whole fan-out every round, or, worse, turn its error into an empty — i.e.
# clean — review.
#
# "A git work tree of its own" is is-git-worktree.sh's test: a colocated
# jj+git repo passes, a git repo in some parent directory does not.
set -eu

choices="${1:?check-reviewers.sh: reviewer_choices argument required}"

wants_codex=$(printf '%s' "$choices" | jq -r 'if type == "array" then (index("codex") != null) else false end')
if [ "$wants_codex" != "true" ]; then
  echo ok
  exit 0
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if "$script_dir/is-git-worktree.sh"; then
  echo ok
else
  echo "check-reviewers.sh: codex was picked, but $(pwd -P) is not a git work tree (a non-colocated jj workspace?); codex review needs one — pick again without codex, or run from a git or colocated jj checkout" >&2
  echo codex_needs_git
fi
