#!/usr/bin/env sh
# review-route.sh <findings_claude> <findings_codex> <findings_custom> <findings_docs> <declined> <minor_findings>
#
# Body of the `review_route` deterministic step: merges the four parallel
# branches' findings arrays (a reviewer branch returns `[]` when its engine
# wasn't selected; the docs branch always runs), drops anything already
# declined by the fixer (record-declined.sh is what actually records a
# decline, from fix_issues' verify_status), then splits what's left by
# severity: a "blocking" finding is routed to fix_issues; a "minor" one is
# never sent to fix_issues at all — it's just accumulated (across rounds,
# deduplicated) and surfaced in the terminal messages, so a narrow, true but
# non-blocking finding no longer keeps the loop from converging on `done`.
#
# The declined-drop is an exact match on the triple (file, category,
# description): a reviewer that disagrees with a decline and wants to
# re-raise the item is expected (per the branch steps' descriptions) to
# explain why in `description`, which changes that triple and lets it back
# through on purpose — this is intentionally not a fuzzy match.
#
# Severity fail-safe: a finding with severity exactly "minor" is minor;
# everything else — "blocking", missing, or any unrecognised value — counts
# as blocking. Unknown means blocking, not "ignore it".
#
# <minor_findings> is the prior round's accumulated minor-findings array
# (state key `minor_findings`, default []). The new accumulated list is the
# prior items plus this round's minor items, deduplicated on the triple
# (file, category, description) with the latest occurrence winning (same
# dedup convention as record-declined.sh: reverse + group_by + take-first +
# reverse, since group_by's sort is stable within equal keys).
#
# Prints "<TOKEN> {\"findings\": [...], \"minor_findings\": [...]}" on one
# line — a routed TOKEN (blocking/clean) followed by a JSON payload (emits:
# json, the default). JSON rather than `pairs` because finding descriptions
# routinely contain spaces, which the pairs grammar can't carry; `jq -c`
# keeps it on the one line the engine parses (format-spec §B.1).
set -eu

findings_claude="${1:?review-route.sh: findings_claude argument required}"
findings_codex="${2:?review-route.sh: findings_codex argument required}"
findings_custom="${3:?review-route.sh: findings_custom argument required}"
findings_docs="${4:?review-route.sh: findings_docs argument required}"
declined="${5:?review-route.sh: declined argument required}"
minor_findings="${6:?review-route.sh: minor_findings argument required}"

result=$(jq -cn \
  --argjson a "$findings_claude" \
  --argjson b "$findings_codex" \
  --argjson c "$findings_custom" \
  --argjson d "$findings_docs" \
  --argjson declined "$declined" \
  --argjson prior_minor "$minor_findings" \
  '($a + $b + $c + $d) as $merged
   | ($declined | map([.file, .category, .description])) as $declined_keys
   | ($merged | map(select(([.file, .category, .description] as $k | $declined_keys | any(. == $k)) | not))) as $filtered
   | ($filtered | map(select(.severity != "minor"))) as $blocking_items
   | ($filtered | map(select(.severity == "minor"))) as $minor_items
   | (($prior_minor + $minor_items)
      | reverse
      | group_by([.file, .category, .description])
      | map(.[0])
      | reverse) as $accumulated_minor
   | {blocking_items: $blocking_items, findings: $blocking_items, minor_findings: $accumulated_minor}')

blocking_count=$(printf '%s' "$result" | jq '.blocking_items | length')
payload=$(printf '%s' "$result" | jq -c '{findings, minor_findings}')

if [ "$blocking_count" -gt 0 ]; then
  printf 'blocking %s\n' "$payload"
else
  printf 'clean %s\n' "$payload"
fi
