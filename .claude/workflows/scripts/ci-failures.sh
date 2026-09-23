#!/usr/bin/env sh
# ci-failures.sh <pr_number> <head_sha>
#
# Body of the `ci_failure` deterministic step, reached when wait_for_ci saw a
# failing check. Turns each failing check into a finding for fix_issues —
# name, link and, for a GitHub Actions job, the tail of its failed-step log
# (`gh run view --job <id> --log-failed`) — so the fixer is told what
# actually broke instead of being sent back to diff-only reviewers. Every
# finding (including the fallback one below) carries severity: "blocking" —
# a failing CI check is never minor.
#
# Prints {"findings": [...], "ci_round": true} on one line (emits: json;
# `jq -c` keeps it on the one line the engine parses). `ci_round: true`
# marks this round as CI-originated, so unchanged_route can tell (if
# fix_push later reports the tree unchanged) that a decline here means CI
# on this head will stay red, not that a re-review is worth another try.
# If the failing checks can't be listed any more (e.g. re-run in the
# meantime) it still emits one finding saying so, so a CI failure never
# turns into an empty fix round.
set -eu

pr_number="${1:?ci-failures.sh: pr_number argument required}"
head_sha="${2:?ci-failures.sh: head_sha argument required}"
log_lines="${PAWL_CI_LOG_LINES:-80}"

rollup=$(gh pr view "$pr_number" --json statusCheckRollup)

failed=$(printf '%s' "$rollup" | jq -c '
  (.statusCheckRollup // [])[]
  | if (.__typename == "StatusContext") or (has("state") and (has("status") | not)) then
      select(.state != "SUCCESS" and .state != "PENDING" and .state != "EXPECTED")
      | {name: (.context // "status"), url: (.targetUrl // "")}
    else
      select(.status == "COMPLETED"
             and .conclusion != "SUCCESS" and .conclusion != "NEUTRAL" and .conclusion != "SKIPPED")
      | {name: ((.workflowName // "") + (if .workflowName then " / " else "" end) + (.name // "check")),
         url: (.detailsUrl // "")}
    end')

items=""
nl='
'
while IFS= read -r check; do
  [ -n "$check" ] || continue
  name=$(printf '%s' "$check" | jq -r '.name')
  url=$(printf '%s' "$check" | jq -r '.url')
  job_id=$(printf '%s' "$url" | sed -n 's#.*/actions/runs/[0-9][0-9]*/job/\([0-9][0-9]*\).*#\1#p')
  if [ -n "$job_id" ] && log=$(gh run view --job "$job_id" --log-failed 2>&1); then
    log=$(printf '%s\n' "$log" | tail -n "$log_lines")
  elif [ -n "$job_id" ]; then
    log="(could not fetch the job log: $(printf '%s' "$log" | tail -n 5))"
  else
    log="(no GitHub Actions job log available — open the check's link)"
  fi
  item=$(jq -cn --arg name "$name" --arg url "$url" --arg sha "$head_sha" --arg log "$log" '{
    file: ("CI: " + $name),
    line: 0,
    category: "bug",
    description: ("CI check \"" + $name + "\" failed on " + $sha
                  + (if $url != "" then " (" + $url + ")" else "" end)
                  + ". Failed-step log tail:\n" + $log),
    fix: "Reproduce the failure locally and change the code (or the test, if the test is wrong) so this check passes.",
    severity: "blocking"
  }')
  items="${items}${item}${nl}"
done <<EOF
$failed
EOF

if [ -z "$items" ]; then
  items=$(jq -cn --arg sha "$head_sha" --arg pr "$pr_number" '{
    file: "CI",
    line: 0,
    category: "bug",
    description: ("CI reported a failure on " + $sha + " but no failing check could be listed any more (re-run or removed?)."),
    fix: ("Inspect `gh pr checks " + $pr + "`, fix whatever is failing, or leave the tree unchanged if CI is actually green."),
    severity: "blocking"
  }')
fi

printf '%s\n' "$items" | jq -cs '{findings: ., ci_round: true}'
