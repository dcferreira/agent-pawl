#!/usr/bin/env sh
# rerun-ci.sh <pr_number> <head_sha> <repo>
#
# Body of the `rerun_ci` deterministic step, reached when wait_for_ci's
# poll-ci.sh said RERUN: nothing failed or is still running for <head_sha>,
# but some GitHub Actions jobs were CANCELLED (a GitHub infrastructure
# hiccup, or a run superseded by `concurrency: cancel-in-progress`). That says
# nothing about the code, so instead of sending the fixer after it, this
# re-runs them: `gh run rerun <run> --failed` re-runs a run's failed AND
# cancelled jobs.
#
# Tied to <head_sha>, like poll-ci.sh/ci-failures.sh: if the PR's head
# (headRefOid) is no longer <head_sha>, the rollup describes another commit's
# checks, so the step fails (non-zero exit -> blocked) rather than re-running
# someone else's jobs.
#
# Steps:
#   1. Collect the COMPLETED + CANCELLED CheckRuns and extract each one's run
#      id from its detailsUrl (.../actions/runs/<run>/job/<job>); several
#      cancelled jobs of one run share a run id and are re-run once.
#   2. A cancelled check with no run id is not a GitHub Actions job and cannot
#      be re-run from here -> stderr naming it, exit 1.
#   3. No cancelled checks any more (someone re-ran them meanwhile) -> stderr
#      note, exit 0: back to wait_for_ci.
#   4. `gh run rerun <run> --failed` for each run id; a failure prints gh's
#      output to stderr and exits 1.
#   5. Settle: GitHub's rollup can keep showing the old CANCELLED conclusion
#      for a few seconds after the rerun request. If wait_for_ci saw that it
#      would answer RERUN again, burning rerun_ci's visit bound (and `gh run
#      rerun` errors on a now-in-progress run). So the rollup is polled every
#      PAWL_CI_RERUN_SETTLE_EVERY seconds (default 5), for up to
#      PAWL_CI_RERUN_SETTLE seconds (default 120), until no COMPLETED +
#      CANCELLED CheckRun belonging to the re-run set remains; a failing `gh
#      pr view` while settling just counts as not settled yet. If it never
#      settles: stderr message, exit 1 (the step fails -> blocked, which beats
#      looping).
#
# Prints nothing on stdout (the step has `next:` and no `writes:`); what was
# re-run is logged to stderr.
set -u

pr_number="${1:?rerun-ci.sh: pr_number argument required}"
head_sha="${2:?rerun-ci.sh: head_sha argument required}"
repo="${3:?rerun-ci.sh: repo argument required}"
settle_max="${PAWL_CI_RERUN_SETTLE:-120}"
settle_every="${PAWL_CI_RERUN_SETTLE_EVERY:-5}"

# Resolved once by fetch-pr.sh from origin's URL; used here instead of
# letting gh infer a repo from cwd (there is no git repo to infer from in a
# non-colocated jj workspace).
export GH_REPO="$repo"

if ! rollup=$(gh pr view "$pr_number" --json headRefOid,statusCheckRollup 2>&1); then
  echo "rerun-ci.sh: gh pr view failed: ${rollup}" >&2
  exit 1
fi

pr_head=$(printf '%s' "$rollup" | jq -r '.headRefOid // ""')
if [ "$pr_head" != "$head_sha" ]; then
  echo "rerun-ci.sh: PR #${pr_number}'s head is now ${pr_head:-<unknown>}, not ${head_sha} (the head wait_for_ci saw cancelled checks on) — its checks belong to another commit; refusing to re-run them as ${head_sha}'s" >&2
  exit 1
fi

# cancelled_checks <rollup json>: one {name, url} JSON object per line for
# every COMPLETED + CANCELLED CheckRun.
cancelled_checks() {
  printf '%s' "$1" | jq -c '
    def latest_runs:
      def is_run: (.__typename != "StatusContext") and has("status");
      (.statusCheckRollup // []) as $r
      | ([$r[] | select(is_run)] | group_by([.workflowName, .name]) | map(max_by(.startedAt // "")))
        + [$r[] | select(is_run | not)];
    latest_runs[]
    | select((.__typename != "StatusContext") and (has("status"))
             and .status == "COMPLETED" and .conclusion == "CANCELLED")
    | {name: ((.workflowName // "") + (if .workflowName then " / " else "" end) + (.name // "check")),
       url: (.detailsUrl // "")}'
}

# run_id_of <url>: the Actions run id in a job URL, or empty.
run_id_of() {
  printf '%s' "$1" | sed -n 's#.*/actions/runs/\([0-9][0-9]*\)/job/[0-9][0-9]*.*#\1#p'
}

cancelled=$(cancelled_checks "$rollup")
if [ -z "$cancelled" ]; then
  echo "rerun-ci.sh: no cancelled checks on ${head_sha} any more (re-run in the meantime?); going back to waiting" >&2
  exit 0
fi

run_ids=""
while IFS= read -r check; do
  [ -n "$check" ] || continue
  name=$(printf '%s' "$check" | jq -r '.name')
  url=$(printf '%s' "$check" | jq -r '.url')
  run_id=$(run_id_of "$url")
  if [ -z "$run_id" ]; then
    echo "rerun-ci.sh: cancelled check \"${name}\" (${url:-no link}) is not a GitHub Actions job, so it cannot be re-run from here" >&2
    exit 1
  fi
  case " $run_ids " in
    *" $run_id "*) ;;
    *) run_ids="${run_ids}${run_ids:+ }${run_id}" ;;
  esac
done <<EOF
$cancelled
EOF

for run_id in $run_ids; do
  if ! out=$(gh run rerun "$run_id" --failed 2>&1); then
    echo "rerun-ci.sh: gh run rerun ${run_id} --failed failed: ${out}" >&2
    exit 1
  fi
  echo "rerun-ci.sh: re-ran the failed/cancelled jobs of run ${run_id} on ${head_sha}" >&2
done

# unsettled <rollup json>: prints "yes" while a cancelled check of a re-run
# run is still reported, nothing otherwise.
unsettled() {
  cancelled_checks "$1" | while IFS= read -r check; do
    [ -n "$check" ] || continue
    rid=$(run_id_of "$(printf '%s' "$check" | jq -r '.url')")
    case " $run_ids " in
      *" $rid "*) echo yes; break ;;
    esac
  done
}

waited=0
while :; do
  if cur=$(gh pr view "$pr_number" --json headRefOid,statusCheckRollup 2>/dev/null) \
    && [ -z "$(unsettled "$cur")" ]; then
    echo "rerun-ci.sh: re-run settled after ${waited}s" >&2
    exit 0
  fi
  if [ "$waited" -ge "$settle_max" ]; then
    echo "rerun-ci.sh: the rollup still showed cancelled checks for run(s) ${run_ids} ${settle_max}s after the re-run request" >&2
    exit 1
  fi
  sleep "$settle_every"
  waited=$((waited + settle_every))
done
