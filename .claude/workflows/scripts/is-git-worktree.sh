#!/usr/bin/env sh
# is-git-worktree.sh
#
# Exits 0 iff the current directory is the top of a git work tree of its
# own: `git rev-parse --show-toplevel` succeeds AND names this directory
# (symlinks resolved), so an unrelated git repo in a parent directory does
# not count. True for a plain git checkout and a colocated jj+git repo;
# false for a non-colocated jj workspace (a .jj directory, no .git). Shared
# by check-reviewers.sh and prepare-review.sh, which both need to know
# whether `codex review --base <git ref>` can run here.
set -eu

top=$(git rev-parse --show-toplevel 2>/dev/null) || exit 1
[ "$(CDPATH= cd -- "$top" && pwd -P)" = "$(pwd -P)" ]
