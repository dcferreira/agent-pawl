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
# `remaining` = findings whose verify_status does NOT start with "wont-fix"
# (i.e. genuinely not-fixed/partially-fixed/fixed-but-apparently-no-op
# items, since the tree didn't move). If any remain, no amount of re-review
# will change anything the fixer couldn't already do: stuck. If none
# remain (every non-fix was a decline) and this was a CI round, CI on this
# head will stay red forever (the failures were all declined, not fixed):
# also stuck, for a human to look at. Otherwise (a review round whose only
# non-fixes were declines): the tree is fine, move on to check CI.
#
# Prints just the TOKEN (to_ci|stuck) on the last stdout line — no
# payload, nothing to write (emits: json, the default, but a TOKEN-only
# line is valid under it per format-spec §B.1). A one-line human-readable
# reason goes to stderr for the logs.
set -eu

findings="${1:?unchanged-route.sh: findings argument required}"
ci_round="${2:?unchanged-route.sh: ci_round argument required}"

remaining=$(printf '%s' "$findings" | jq -c '[.[] | select((.verify_status // "") | startswith("wont-fix") | not)]')
remaining_count=$(printf '%s' "$remaining" | jq 'length')

if [ "$remaining_count" -gt 0 ]; then
  echo "unchanged-route.sh: ${remaining_count} finding(s) left unresolved (not declined) and the tree didn't change — stuck" >&2
  echo stuck
elif [ "$ci_round" = "true" ]; then
  echo "unchanged-route.sh: all findings were declined but this was a CI-originated round — CI on this head will stay red, stuck" >&2
  echo stuck
else
  echo "unchanged-route.sh: all findings were declined and this was a review round — on to CI" >&2
  echo to_ci
fi
