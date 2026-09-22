#!/usr/bin/env sh
# poll-ci.sh <pr_number>
#
# `poll:` body for the `wait_for_ci` step. `gh pr checks` categorizes each
# check into a `bucket` (pass|fail|pending|skipping|cancel) and, per its own
# --help text, exits non-zero while checks are still pending (and also
# when a PR has no checks configured at all) — but design/format-spec.md
# §B.1 says a non-zero exit is unconditionally `failure`, which would kill
# this wait step outright instead of letting it keep polling. So the gh
# call's exit status is deliberately ignored and PENDING/SUCCESS/FAILURE is
# derived from its JSON output (or the absence of any).
#
# Prints one of PASSED | FAILURE | PENDING. PENDING is not one of
# wait_for_ci's declared `outcomes:` — following the same convention as
# examples/wait-for-build/scripts/check_build.sh, an unrouted token is
# simply logged and `pawl poll` re-runs this script on the next tick.
# Always exits 0 (see above).
set -eu

pr_number="${1:?poll-ci.sh: pr_number argument required}"

out=$(gh pr checks "$pr_number" --json bucket 2>/dev/null) || out=""

if [ -z "$out" ] || [ "$out" = "[]" ]; then
  # No checks configured on this PR — nothing to wait for.
  echo SUCCESS
elif echo "$out" | jq -e 'any(.[]; .bucket == "fail")' >/dev/null 2>&1; then
  echo FAILURE
elif echo "$out" | jq -e 'any(.[]; .bucket == "pending")' >/dev/null 2>&1; then
  echo PENDING
else
  echo SUCCESS
fi

exit 0
