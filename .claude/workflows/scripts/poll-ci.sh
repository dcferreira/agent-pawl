#!/usr/bin/env sh
# poll-ci.sh <pr_number> <head_sha> <allow_no_ci> <repo>
#
# `poll:` body for the `wait_for_ci` step: the CI verdict for exactly
# <head_sha> (the head this round reviewed), never for whatever GitHub last
# happened to report.
#
# Reads `gh pr view --json headRefOid,statusCheckRollup` and prints one of
# SUCCESS | FAILURE | PENDING | RERUN:
#   - gh fails (auth, network, ...)          -> PENDING (logged to stderr)
#   - headRefOid != head_sha                 -> PENDING (GitHub hasn't caught
#     up with the push yet, or someone else pushed; the latter runs into
#     wait_for_ci's timeout -> blocked rather than passing silently)
#   - no checks registered for the head      -> PENDING, unless allow_no_ci is
#     "true" (a repo with no CI at all), then SUCCESS. Checks can take a
#     moment to appear after a push, so "none yet" is not "green".
#   - any check failed, errored, timed out, or needs action
#                                            -> FAILURE
#   - any check still queued/running         -> PENDING
#   - any check was cancelled (and none of the above)
#                                            -> RERUN
#   - otherwise (success/neutral/skipped)    -> SUCCESS, subject to the two
#     rules below
#
# Expected set: "every check registered so far passed" is not enough, since
# right after a push GitHub may have registered only some workflows' checks
# (e.g. Changelog's, while CI's jobs have not appeared yet). So SUCCESS also
# needs every *expected* check name present for the head and passing; a
# missing one is PENDING. Expected names come from the base branch's required
# status checks (classic protection and rulesets; a 404 or empty answer, the
# case for this repo, means "none"), else from the fallback list in
# PAWL_REVIEW_EXPECTED_CHECKS (newline- or comma-separated; default: this
# repo's three job names). A SKIPPED conclusion counts as present and passing
# (the Changelog job still runs and passes under `skip changelog`). With
# allow_no_ci=true and zero checks registered, SUCCESS stands as above and the
# expected set is not required (a repo without CI).
#
# Settle time: a green verdict is held as PENDING until
# PAWL_REVIEW_CI_SETTLE_SECONDS (default 30) have passed since this script
# first saw <head_sha> as the PR's head, covering workflows that register late.
# The script is stateless between polls, so the first-seen epoch is kept in a
# file under the cache dir, keyed by repo+PR+SHA. If that file cannot be
# written, this logs to stderr and skips the settle wait rather than hanging.
#
# Why cancelled is its own verdict: a CANCELLED job says nothing about the
# code — it is a GitHub infrastructure hiccup, or a run superseded by this
# repo's `concurrency: cancel-in-progress` — so feeding it to the fixer as a
# "failure" sends it to fix code that isn't broken, with an empty log tail.
# RERUN routes to rerun_ci instead, which re-runs the cancelled jobs. A
# timed-out job stays FAILURE (it can be a genuinely hung test), as does
# startup_failure (usually an invalid workflow file): both are things the
# fixer can act on. Precedence is deliberate: a real failure wins over a
# cancellation (the fixer's push starts a fresh CI run anyway), and anything
# still running is awaited first (PENDING before RERUN) so rerun_ci never
# meets an in-progress run, which `gh run rerun` refuses. A StatusContext has
# no cancelled state, so its handling is unchanged.
#
# PENDING is not one of wait_for_ci's declared `outcomes:` — following the
# same convention as docs/examples/wait-for-build/scripts/check_build.sh, an
# unrouted token is simply logged and `pawl poll` re-runs this script on
# the next tick. Always exits 0: design/format-spec.md §B.1 makes a non-zero
# exit unconditionally `failure`, which would end the wait outright.
set -u

pr_number="${1:?poll-ci.sh: pr_number argument required}"
head_sha="${2:?poll-ci.sh: head_sha argument required}"
allow_no_ci="${3:-false}"
repo="${4:?poll-ci.sh: repo argument required}"

# Resolved once by fetch-pr.sh from origin's URL; used here instead of
# letting gh infer a repo from cwd (there is no git repo to infer from in a
# non-colocated jj workspace).
export GH_REPO="$repo"

settle="${PAWL_REVIEW_CI_SETTLE_SECONDS:-30}"
case "$settle" in
  '' | *[!0-9]*) settle=30 ;;
esac
default_expected='Lint (gofmt + vet)
Test (unit + e2e)
Fragment + no hand-bumped version'

if ! out=$(gh pr view "$pr_number" --json headRefOid,baseRefName,statusCheckRollup 2>&1); then
  echo "poll-ci.sh: gh pr view failed, will retry: ${out}" >&2
  echo PENDING
  exit 0
fi

# Required checks of the PR's base branch, one name per line: classic branch
# protection (contexts + checks) and rulesets. Any error (404 "Branch not
# protected", no permission, network) or an empty answer means "none".
required_checks() {
  base=$(printf '%s' "$out" | jq -r '.baseRefName // empty' 2>/dev/null) || return 0
  [ -n "$base" ] || return 0
  {
    gh api "repos/${repo}/branches/${base}/protection/required_status_checks" 2>/dev/null \
      | jq -r '((.contexts // []) + [(.checks // [])[].context]) | .[]' 2>/dev/null
    gh api "repos/${repo}/rules/branches/${base}" 2>/dev/null \
      | jq -r '.[]? | select(.type == "required_status_checks") | .parameters.required_status_checks[]?.context' 2>/dev/null
  } | sort -u
}

expected=$(required_checks)
if [ -z "$expected" ]; then
  expected="${PAWL_REVIEW_EXPECTED_CHECKS:-$default_expected}"
  expected=$(printf '%s\n' "$expected" | tr ',' '\n' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//' | sed '/^$/d')
fi

verdict=$(printf '%s' "$out" | jq -r --arg sha "$head_sha" --arg allow "$allow_no_ci" --arg expected "$expected" '
  def bucket:
    if (.__typename == "StatusContext") or (has("state") and (has("status") | not)) then
      if .state == "SUCCESS" then "pass"
      elif .state == "PENDING" or .state == "EXPECTED" then "pending"
      else "fail" end
    else
      if .status != "COMPLETED" then "pending"
      elif .conclusion == "CANCELLED" then "cancelled"
      elif .conclusion == "SUCCESS" or .conclusion == "NEUTRAL" or .conclusion == "SKIPPED" then "pass"
      else "fail" end
    end;
  # Only the newest run of each (workflow, check) counts: a superseded
  # (e.g. cancel-in-progress) run stays in the rollup next to its successor.
  def latest_runs:
    def is_run: (.__typename != "StatusContext") and has("status");
    (.statusCheckRollup // []) as $r
    | ([$r[] | select(is_run)] | group_by([.workflowName, .name]) | map(max_by([(.status != "COMPLETED"), (.startedAt // "")])))
      + [$r[] | select(is_run | not)];
  if .headRefOid != $sha then "PENDING head-mismatch"
  else
    [latest_runs[] | bucket] as $b
    | [latest_runs[] | select(bucket == "pass") | (.name // .context)] as $passed
    | ($expected | split("\n") | map(select(. != ""))) as $exp
    | if ($b | length) == 0 then
        (if $allow == "true" then "SUCCESS no-ci" else "PENDING no-checks" end)
      elif any($b[]; . == "fail") then "FAILURE"
      elif any($b[]; . == "pending") then "PENDING"
      elif any($b[]; . == "cancelled") then "RERUN"
      # Expected-but-absent (or not passing, e.g. only a cancelled sibling
      # of the name exists) is not yet green.
      elif any($exp[]; . as $n | $passed | index($n) | not) then "PENDING missing-expected"
      else "SUCCESS" end
  end
' 2>/dev/null) || verdict="PENDING unparseable"
[ -n "$verdict" ] || verdict="PENDING unparseable"

# First-seen time of this head, recorded on the first poll that sees it as the
# PR's head (green or not), so the settle clock runs while checks register.
first_seen() {
  cache_dir="${XDG_CACHE_HOME:-${HOME}/.cache}/pawl-review-pr"
  key=$(printf '%s' "${repo}-${pr_number}-${head_sha}" | tr -c 'A-Za-z0-9._-' '_')
  state_file="${cache_dir}/ci-seen-${key}"
  now=$(date +%s)
  if [ -r "$state_file" ] && seen=$(cat "$state_file" 2>/dev/null) && [ -n "$seen" ] \
    && [ -z "$(printf '%s' "$seen" | tr -d '0-9')" ]; then
    echo "$seen"
    return 0
  fi
  if mkdir -p "$cache_dir" 2>/dev/null && printf '%s\n' "$now" >"$state_file" 2>/dev/null; then
    echo "$now"
  else
    echo "poll-ci.sh: cannot write ${state_file}; skipping settle time" >&2
    echo "$now" # elapsed 0 would hang, so report "no settle" below instead
    return 1
  fi
}

case "$verdict" in
  "PENDING head-mismatch" | "PENDING unparseable") ;;
  *)
    if seen=$(first_seen); then
      elapsed=$(( $(date +%s) - seen ))
      if [ "$verdict" = "SUCCESS" ] && [ "$elapsed" -lt "$settle" ]; then
        echo "poll-ci.sh: checks green, settling (${elapsed}s of ${settle}s since ${head_sha} first seen)" >&2
        verdict="PENDING settle"
      fi
    fi
    ;;
esac

case "$verdict" in
  "PENDING head-mismatch")
    echo "poll-ci.sh: PR #${pr_number}'s head is not ${head_sha} yet" >&2
    ;;
  "PENDING no-checks")
    echo "poll-ci.sh: no checks registered for ${head_sha} yet (pass allow_no_ci=true for a repo without CI)" >&2
    ;;
  "PENDING missing-expected")
    echo "poll-ci.sh: expected checks not all present and passing for ${head_sha} yet: $(printf '%s' "$expected" | tr '\n' ';')" >&2
    ;;
  "PENDING unparseable")
    echo "poll-ci.sh: could not parse gh output: ${out}" >&2
    ;;
esac

echo "${verdict%% *}"
exit 0
