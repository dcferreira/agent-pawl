#!/usr/bin/env sh
# review-route.sh <findings_claude> <findings_codex> <findings_custom> <findings_docs>
#
# Body of the `review_route` deterministic step: merges the four parallel
# branches' findings arrays (a reviewer branch returns `[]` when its engine
# wasn't selected; the docs branch always runs) and decides whether another
# fix round is needed.
#
# Prints "<TOKEN> {\"findings\": [...]}" on one line — a routed TOKEN
# (blocking/clean) followed by a JSON payload (emits: json, the default).
# JSON rather than `pairs` because finding descriptions routinely contain
# spaces, which the pairs grammar can't carry; `jq -c` keeps it on the one
# line the engine parses (format-spec §B.1).
set -eu

findings_claude="${1:?review-route.sh: findings_claude argument required}"
findings_codex="${2:?review-route.sh: findings_codex argument required}"
findings_custom="${3:?review-route.sh: findings_custom argument required}"
findings_docs="${4:?review-route.sh: findings_docs argument required}"

merged=$(jq -cn \
  --argjson a "$findings_claude" \
  --argjson b "$findings_codex" \
  --argjson c "$findings_custom" \
  --argjson d "$findings_docs" \
  '$a + $b + $c + $d')

if [ "$(printf '%s' "$merged" | jq 'length')" -gt 0 ]; then
  printf 'blocking {"findings": %s}\n' "$merged"
else
  printf 'clean {"findings": %s}\n' "$merged"
fi
