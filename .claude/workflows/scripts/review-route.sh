#!/usr/bin/env sh
# review-route.sh <findings_claude> <findings_codex> <findings_custom> <findings_docs> <declined>
#
# Body of the `review_route` deterministic step: merges the four parallel
# branches' findings arrays (a reviewer branch returns `[]` when its engine
# wasn't selected; the docs branch always runs), drops anything already
# declined by the fixer (record-declined.sh is what actually records a
# decline, from fix_issues' verify_status), and decides whether another fix
# round is needed on what's left.
#
# The drop is an exact match on the triple (file, category, description): a
# reviewer that disagrees with a decline and wants to re-raise the item is
# expected (per the branch steps' descriptions) to explain why in
# `description`, which changes that triple and lets it back through on
# purpose — this is intentionally not a fuzzy match.
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
declined="${5:?review-route.sh: declined argument required}"

filtered=$(jq -cn \
  --argjson a "$findings_claude" \
  --argjson b "$findings_codex" \
  --argjson c "$findings_custom" \
  --argjson d "$findings_docs" \
  --argjson declined "$declined" \
  '($a + $b + $c + $d) as $merged
   | ($declined | map([.file, .category, .description])) as $declined_keys
   | $merged | map(select(([.file, .category, .description] as $k | $declined_keys | any(. == $k)) | not))')

if [ "$(printf '%s' "$filtered" | jq 'length')" -gt 0 ]; then
  printf 'blocking {"findings": %s}\n' "$filtered"
else
  printf 'clean {"findings": %s}\n' "$filtered"
fi
