#!/usr/bin/env sh
# review-route.sh <findings_claude> <findings_codex> <findings_custom> <findings_docs> <declined>
#
# Body of the `review_route` deterministic step: merges the four parallel
# branches' findings arrays (a reviewer branch returns `[]` when its engine
# wasn't selected; the docs branch always runs), drops anything already
# declined by the fixer (record-declined.sh is what actually records a
# decline, from fix_issues' verify_status), then splits what's left by
# severity on the critical/major/medium/minor/nitpick scale:
#   - "fixable": everything whose severity is NOT exactly "nitpick" —
#     critical, major, medium, minor, and (fail-safe) missing or
#     unrecognised severities all count as fixable. Unknown means fixable,
#     not "ignore it".
#   - "nits": severity exactly "nitpick".
#
# The declined-drop is an exact match on the triple (file, category,
# description): a reviewer that disagrees with a decline and wants to
# re-raise the item is expected (per the branch steps' descriptions) to
# explain why in `description`, which changes that triple and lets it back
# through on purpose — this is intentionally not a fuzzy match.
#
# Unlike the old minor-findings scheme, nitpicks are NOT accumulated across
# rounds here — this round's nits are simply written to the `nitpicks` state
# key and, if there's nothing fixable, handed to the ask_nitpicks human step
# this same round. If fixable is non-empty, the nitpicks are held (written
# to state) but not acted on until a later round has nothing fixable left.
#
# Prints one of three routed lines (a TOKEN followed by a JSON payload,
# emits: json — `jq -c` keeps it on the one line the engine parses,
# format-spec §B.1):
#   - fixable non-empty:        "blocking      {findings: fixable, nitpicks: nits}"
#   - fixable empty, nits not:  "nitpicks_only {findings: [],      nitpicks: nits}"
#   - both empty:               "clean         {findings: [],      nitpicks: []}"
set -eu

findings_claude="${1:?review-route.sh: findings_claude argument required}"
findings_codex="${2:?review-route.sh: findings_codex argument required}"
findings_custom="${3:?review-route.sh: findings_custom argument required}"
findings_docs="${4:?review-route.sh: findings_docs argument required}"
declined="${5:?review-route.sh: declined argument required}"

result=$(jq -cn \
  --argjson a "$findings_claude" \
  --argjson b "$findings_codex" \
  --argjson c "$findings_custom" \
  --argjson d "$findings_docs" \
  --argjson declined "$declined" \
  '($a + $b + $c + $d) as $merged
   | ($declined | map([.file, .category, .description])) as $declined_keys
   | ($merged | map(select(([.file, .category, .description] as $k | $declined_keys | any(. == $k)) | not))) as $filtered
   | ($filtered | map(select(.severity != "nitpick"))) as $fixable
   | ($filtered | map(select(.severity == "nitpick"))) as $nits
   | {fixable: $fixable, nits: $nits}')

fixable_count=$(printf '%s' "$result" | jq '.fixable | length')
nits_count=$(printf '%s' "$result" | jq '.nits | length')

if [ "$fixable_count" -gt 0 ]; then
  payload=$(printf '%s' "$result" | jq -c '{findings: .fixable, nitpicks: .nits}')
  printf 'blocking %s\n' "$payload"
elif [ "$nits_count" -gt 0 ]; then
  payload=$(printf '%s' "$result" | jq -c '{findings: [], nitpicks: .nits}')
  printf 'nitpicks_only %s\n' "$payload"
else
  printf 'clean %s\n' '{"findings": [], "nitpicks": []}'
fi
