#!/usr/bin/env sh
# take-optional.sh <fix_input_file> <optional_findings>
#
# Body of the `take_optional` deterministic step, reached when ask_optional's
# answer was "address": the held findings (the `optional_findings` state
# key — review-route.sh's rendered view of the ledger's "held" entries, each
# already carrying its ledger `id`) become the findings fix_issues fixes.
# Turns <optional_findings> into `{findings: ..., optional_findings: []}`
# (fix_issues fixes them now; the held list is emptied here — record-
# dispositions.sh updates each item's ledger entry by `id` once fix_issues
# returns, same as any other round) and, like review-route.sh's `blocking`
# branch, overwrites <fix_input_file> (atomically) with the same array: a
# snapshot of exactly what fix_issues is about to be asked to fix, read back
# by check-fix-result.sh (fix_issues' postcondition) to confirm nothing
# given to the fixer gets silently dropped from its returned findings.
#
# Per the top-of-file round-cap comment: this is what keeps "address a held
# nit" from ever re-triggering a full re-review — the round that follows
# fix_push re-enters prepare_review with `last_reviewed_head` already
# advanced past this head, so it comes back as a delta round, scoped to
# just this fix, never a fresh full review.
#
# Prints {"findings": ..., "optional_findings": []} on one line (emits:
# json).
set -eu

fix_input_file="${1:?take-optional.sh: fix_input_file argument required}"
optional_findings="${2:?take-optional.sh: optional_findings argument required}"

printf '%s' "$optional_findings" | jq -c '.' >"${fix_input_file}.tmp"
mv "${fix_input_file}.tmp" "$fix_input_file"

jq -cn --argjson n "$optional_findings" '{findings: $n, optional_findings: []}'
