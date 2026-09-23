#!/usr/bin/env sh
# review-route.sh <findings_claude> <findings_codex> <findings_custom> <findings_docs> <nitpicks> <declined_file>
#
# Body of the `review_route` deterministic step: merges the four parallel
# branches' findings arrays (a reviewer branch returns `[]` when its engine
# wasn't selected; the docs branch always runs), drops anything already
# declined by the fixer (record-declined.sh is what actually records a
# decline, from fix_issues' verify_status, into <declined_file>), then
# splits what's left by severity on the critical/major/medium/minor/nitpick
# scale:
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
# <nitpicks> is the `nitpicks` state key: nitpicks held from earlier rounds
# that were never asked about (a round with fixable findings does not reach
# ask_nitpicks). They are merged with this round's fresh nits, deduplicated
# on (file, category, description) — the fresh copy wins — and the declined
# ones dropped, so a nitpick raised next to blocking findings is carried
# forward until a round with nothing fixable asks about it, rather than
# being lost when a later cold reviewer doesn't happen to raise it again.
# take_nitpicks and skip_nitpicks reset the key to [] once it's been asked
# about. A held nitpick a later fix happened to resolve is still asked
# about; the user can skip it.
#
# <declined_file> is read with --slurpfile, never through argv (see
# record-declined.sh for why the declined list is a file).
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
held_nitpicks="${5:?review-route.sh: nitpicks argument required}"
declined_file="${6:?review-route.sh: declined_file argument required}"

result=$(jq -cn \
  --argjson a "$findings_claude" \
  --argjson b "$findings_codex" \
  --argjson c "$findings_custom" \
  --argjson d "$findings_docs" \
  --argjson held "$held_nitpicks" \
  --slurpfile declined "$declined_file" \
  'def key: [.file, .category, .description];
   ($a + $b + $c + $d) as $merged
   | ($declined[0] | map(key)) as $declined_keys
   | def undeclined: map(select((key as $k | $declined_keys | any(. == $k)) | not));
   ($merged | undeclined) as $filtered
   | ($filtered | map(select(.severity != "nitpick"))) as $fixable
   | ($filtered | map(select(.severity == "nitpick"))) as $fresh_nits
   | ($fresh_nits | map(key)) as $fresh_keys
   | ($held | undeclined | map(select((key as $k | $fresh_keys | any(. == $k)) | not))) as $still_held
   | ($still_held + $fresh_nits) as $nits
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
