#!/usr/bin/env sh
# record-declined.sh <declined> <findings>
#
# Body of the `record_declined` deterministic step, run right after
# fix_issues and before fix_push: carries forward every finding the fixer
# has ever declined, so the next round's cold reviewers know not to re-raise
# it without cause (they see it as ${declined}) and review_route can filter
# it back out if they do anyway.
#
# <declined> is the prior round's declined-findings array (state key
# `declined`, default []). <findings> is fix_issues' output: the round's
# findings, each with an added `verify_status`. An item whose verify_status
# starts with "wont-fix" is a decline; everything else (fixed:/
# partially-fixed:/not-fixed:) is not carried forward here — a "not-fixed"
# finding is meant to be raised again.
#
# The new list is the prior declined items plus this round's declines,
# deduplicated on the triple (file, category, description). On a duplicate
# the LATEST occurrence wins (kept via reverse + group_by + take-first,
# since group_by's sort is stable within equal keys), so a re-declined
# finding's newest verify_status/reason is what travels forward, not a
# stale one from an earlier round.
#
# Prints {"declined": [...]} on one line (emits: json, the default; jq -c
# keeps it on the one line the engine parses, format-spec §B.1).
set -eu

declined="${1:?record-declined.sh: declined argument required}"
findings="${2:?record-declined.sh: findings argument required}"

jq -cn --argjson declined "$declined" --argjson findings "$findings" '
  ($findings | map(select((.verify_status // "") | startswith("wont-fix")))) as $new
  | ($declined + $new) as $all
  | {
      declined: (
        $all
        | reverse
        | group_by([.file, .category, .description])
        | map(.[0])
        | reverse
      )
    }
'
