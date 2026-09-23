#!/usr/bin/env sh
# record-declined.sh <declined_file> <findings>
#
# Body of the `record_declined` deterministic step, run right after
# fix_issues and before fix_push: carries forward every finding the fixer
# has ever declined, so the next round's cold reviewers know not to re-raise
# it without cause (they read <declined_file> as context) and review_route
# can filter it back out if they do anyway.
#
# <declined_file> is the path (state key `declined_file`, created holding
# `[]` by fetch-pr.sh) of the JSON array of every finding declined so far.
# It is a file, not a state key, because it grows every round: a state key
# a deterministic step reads is rendered into its one `sh -c` argument and
# exported as PAWL_<KEY>, both capped at 128 KiB (MAX_ARG_STRLEN) on Linux.
# It is read with --slurpfile, never through argv, for the same reason.
# <findings> is fix_issues' output: the round's findings, each with an
# added `verify_status`. An item whose verify_status starts with "wont-fix"
# is a decline; everything else (fixed:/partially-fixed:/not-fixed:) is not
# carried forward here — a "not-fixed" finding is meant to be raised again.
#
# The new list is the prior declined items plus this round's declines,
# deduplicated on the triple (file, category, description). On a duplicate
# the LATEST occurrence wins (kept via reverse + group_by + take-first,
# since group_by's sort is stable within equal keys), so a re-declined
# finding's newest verify_status/reason is what travels forward, not a
# stale one from an earlier round. Re-running it (a resumed step) is
# idempotent: the same declines merge to the same list.
#
# The merged list replaces <declined_file> atomically (temp file + mv).
# Prints nothing to stdout: the step writes no state key.
set -eu

declined_file="${1:?record-declined.sh: declined_file argument required}"
findings="${2:?record-declined.sh: findings argument required}"

jq -cn --slurpfile declined "$declined_file" --argjson findings "$findings" '
  ($findings | map(select((.verify_status // "") | startswith("wont-fix")))) as $new
  | ($declined[0] + $new) as $all
  | $all
  | reverse
  | group_by([.file, .category, .description])
  | map(.[0])
  | reverse
' >"${declined_file}.tmp"
mv "${declined_file}.tmp" "$declined_file"
