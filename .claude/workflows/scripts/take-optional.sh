#!/usr/bin/env sh
# take-optional.sh <fix_input_file> <optional_findings>
#
# Body of the `take_optional` deterministic step, reached when ask_optional's
# answer was "address": the held minor/nitpick findings become the findings
# fix_issues fixes. Turns <optional_findings> into `{findings: ...,
# optional_findings: []}` (fix_issues fixes them now; the held list is
# emptied — anything fix_issues leaves not-fixed is raised again like any
# other finding) and, like review-route.sh's `blocking` branch, overwrites
# <fix_input_file> (atomically) with the same array: a snapshot of exactly
# what fix_issues is about to be asked to fix, read back by
# check-fix-result.sh (fix_issues' postcondition) to confirm nothing given
# to the fixer gets silently dropped from its returned findings.
#
# Prints {"findings": ..., "optional_findings": []} on one line (emits:
# json).
set -eu

fix_input_file="${1:?take-optional.sh: fix_input_file argument required}"
optional_findings="${2:?take-optional.sh: optional_findings argument required}"

printf '%s' "$optional_findings" | jq -c '.' >"${fix_input_file}.tmp"
mv "${fix_input_file}.tmp" "$fix_input_file"

jq -cn --argjson n "$optional_findings" '{findings: $n, optional_findings: []}'
