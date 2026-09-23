#!/usr/bin/env sh
# review-route.sh <findings_claude> <findings_codex> <findings_custom> <findings_docs> <optional_findings> <declined_file> <fix_input_file>
#
# Body of the `review_route` deterministic step: merges the four parallel
# branches' findings arrays (a reviewer branch returns `[]` when its engine
# wasn't selected; the docs branch always runs), drops anything already
# declined by the fixer (record-declined.sh is what actually records a
# decline, from fix_issues' verify_status, into <declined_file>), then
# splits what's left by severity on the critical/major/medium/minor/nitpick
# scale:
#   - "fixable": everything whose severity is NOT "minor" or "nitpick" —
#     critical, major, medium, and (fail-safe) missing or unrecognised
#     severities all count as fixable. Unknown means fixable, not "ignore
#     it". Auto-fixing every minor finding used to grow the workflow by
#     hundreds of lines in a single round, adding new review surface each
#     time it ran — minor is now held and asked about instead, exactly like
#     nitpick.
#   - "optional": severity "minor" or "nitpick" — held across rounds,
#     merged, deduped and declined-filtered the same way, and presented to
#     the user (ask_optional) who chooses to address or skip them.
#
# The declined-drop is an exact match on the triple (file, category,
# description): a reviewer that disagrees with a decline and wants to
# re-raise the item is expected (per the branch steps' descriptions) to
# explain why in `description`, which changes that triple and lets it back
# through on purpose — this is intentionally not a fuzzy match.
#
# <optional_findings> is the `optional_findings` state key: minor/nitpick
# items held from earlier rounds that were never asked about (a round with
# fixable findings does not reach ask_optional). They are merged with this
# round's fresh minor/nitpick items, deduplicated on (file, category,
# description) — the fresh copy wins — and the declined ones dropped, so an
# optional finding raised next to blocking findings is carried forward
# until a round with nothing fixable asks about it, rather than being lost
# when a later cold reviewer doesn't happen to raise it again.
# take_optional and skip_optional reset the key to [] once it's been asked
# about. A held item a later fix happened to resolve is still asked about;
# the user can skip it.
#
# The final optional list is ordered minor first, then nitpick (a stable
# sort on severity rank, so relative order within each severity is
# preserved) — the order ask_optional's question lists them in.
#
# <declined_file> is read with --slurpfile, never through argv (see
# record-declined.sh for why the declined list is a file).
#
# On a `blocking` outcome, also overwrites <fix_input_file> (atomically)
# with the `fixable` array — a snapshot of exactly what fix_issues is about
# to be asked to fix, read back by check-fix-result.sh (fix_issues'
# postcondition) to confirm nothing given to the fixer gets silently
# dropped from its returned findings. Left untouched on `optional_only`/
# `clean` (fix_issues isn't reached either way; take-optional.sh writes it
# for the optional_only -> address path instead).
#
# Prints one of three routed lines (a TOKEN followed by a JSON payload,
# emits: json — `jq -c` keeps it on the one line the engine parses,
# format-spec §B.1):
#   - fixable non-empty:        "blocking      {findings: fixable, optional_findings: optional}"
#   - fixable empty, opt not:   "optional_only {findings: [],      optional_findings: optional}"
#   - both empty:               "clean         {findings: [],      optional_findings: []}"
set -eu

findings_claude="${1:?review-route.sh: findings_claude argument required}"
findings_codex="${2:?review-route.sh: findings_codex argument required}"
findings_custom="${3:?review-route.sh: findings_custom argument required}"
findings_docs="${4:?review-route.sh: findings_docs argument required}"
held_optional="${5:?review-route.sh: optional_findings argument required}"
declined_file="${6:?review-route.sh: declined_file argument required}"
fix_input_file="${7:?review-route.sh: fix_input_file argument required}"

result=$(jq -cn \
  --argjson a "$findings_claude" \
  --argjson b "$findings_codex" \
  --argjson c "$findings_custom" \
  --argjson d "$findings_docs" \
  --argjson held "$held_optional" \
  --slurpfile declined "$declined_file" \
  'def key: [.file, .category, .description];
   def is_optional: .severity == "minor" or .severity == "nitpick";
   def rank: if .severity == "minor" then 0 else 1 end;
   ($a + $b + $c + $d) as $merged
   | ($declined[0] | map(key)) as $declined_keys
   | def undeclined: map(select((key as $k | $declined_keys | any(. == $k)) | not));
   ($merged | undeclined) as $filtered
   | ($filtered | map(select(is_optional | not))) as $fixable
   | ($filtered | map(select(is_optional))) as $fresh_optional
   | ($fresh_optional | map(key)) as $fresh_keys
   | ($held | undeclined | map(select((key as $k | $fresh_keys | any(. == $k)) | not))) as $still_held
   | (($still_held + $fresh_optional) | sort_by(rank)) as $optional
   | {fixable: $fixable, optional: $optional}')

fixable_count=$(printf '%s' "$result" | jq '.fixable | length')
optional_count=$(printf '%s' "$result" | jq '.optional | length')

if [ "$fixable_count" -gt 0 ]; then
  printf '%s' "$result" | jq -c '.fixable' >"${fix_input_file}.tmp"
  mv "${fix_input_file}.tmp" "$fix_input_file"
  payload=$(printf '%s' "$result" | jq -c '{findings: .fixable, optional_findings: .optional}')
  printf 'blocking %s\n' "$payload"
elif [ "$optional_count" -gt 0 ]; then
  payload=$(printf '%s' "$result" | jq -c '{findings: [], optional_findings: .optional}')
  printf 'optional_only %s\n' "$payload"
else
  printf 'clean %s\n' '{"findings": [], "optional_findings": []}'
fi
