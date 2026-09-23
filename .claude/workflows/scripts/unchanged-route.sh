#!/usr/bin/env sh
# unchanged-route.sh <findings> <ci_round>
#
# Body of the `unchanged_route` deterministic step, reached when fix_push
# printed `unchanged`: fix_issues made no change to the tree. That's
# expected when this round's only non-fixes were declines (wont-fix), but
# retrying on an unchanged tree is pointless whenever something the fixer
# could not actually change is still outstanding.
#
# <findings> is fix_issues' returned findings (each item has a
# verify_status). <ci_round> is the `ci_round` state key: true iff this
# round's findings came from ci_failure (a failing CI check), false if they
# came from review_route (reviewer findings).
#
# `remaining` depends on the round:
#   - Review round (ci_round false): findings whose verify_status starts
#     with "not-fixed" or "partially-fixed". A `fixed:` item on an unchanged
#     tree means there was nothing left to change (e.g. a held optional
#     item an earlier fix already resolved, which the user chose to
#     "address"), so it counts as resolved, like a wont-fix decline.
#   - CI round (ci_round true): every finding whose verify_status does NOT
#     start with "wont-fix" — `fixed:` included, since the check really
#     failed on this head and an unchanged tree can't have fixed it.
# If any remain, no amount of re-review will change anything the fixer
# couldn't already do: stuck. If none remain and this was a CI round, CI on
# this head will stay red forever (the failures were all declined, not
# fixed): also stuck, for a human to look at. Otherwise (a review round
# with nothing left unresolved): the tree is fine, move on to check CI.
# This stays bounded: `to_ci` leads through ci_failure, which sets
# ci_round true, so a second `unchanged` in the same round is `stuck`.
#
# Prints just the TOKEN (to_ci|stuck) on the last stdout line — no
# payload, nothing to write (emits: json, the default, but a TOKEN-only
# line is valid under it per format-spec §B.1). A one-line human-readable
# reason goes to stderr for the logs.
set -eu

findings="${1:?unchanged-route.sh: findings argument required}"
ci_round="${2:?unchanged-route.sh: ci_round argument required}"

if [ "$ci_round" = "true" ]; then
  remaining=$(printf '%s' "$findings" | jq -c '[.[] | select((.verify_status // "") | startswith("wont-fix") | not)]')
else
  remaining=$(printf '%s' "$findings" | jq -c '[.[] | select((.verify_status // "") | test("^(not-fixed|partially-fixed)"))]')
fi
remaining_count=$(printf '%s' "$remaining" | jq 'length')

if [ "$remaining_count" -gt 0 ]; then
  echo "unchanged-route.sh: ${remaining_count} finding(s) left unresolved and the tree didn't change — stuck" >&2
  echo stuck
elif [ "$ci_round" = "true" ]; then
  echo "unchanged-route.sh: all findings were declined but this was a CI-originated round — CI on this head will stay red, stuck" >&2
  echo stuck
else
  echo "unchanged-route.sh: nothing left unresolved (declined, or fixed with nothing to change) on a review round — on to CI" >&2
  echo to_ci
fi
