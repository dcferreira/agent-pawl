#!/usr/bin/env sh
# fetch-pr.sh <pr_number>
#
# Body of the `fetch_pr` deterministic step. Prints a single JSON object
# (emits: json, the default) remapping `gh pr view`'s field names onto this
# workflow's state keys, plus this checkout's VCS (jj or git, from
# detect-vcs.sh) — deliberately one JSON object rather than `emits: pairs`,
# since a PR title routinely contains spaces and the pairs grammar
# (whitespace-separated k=v tokens) cannot carry that safely. `jq -c` is
# load-bearing: the engine parses only the last non-empty stdout line
# (format-spec §B.1), and pretty-printed JSON's last line is just `}`.
set -eu

pr_number="${1:?fetch-pr.sh: pr_number argument required}"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

vcs=$("$script_dir/detect-vcs.sh")

gh pr view "$pr_number" --json url,headRefOid,baseRefName,headRefName,title \
  | jq -c --arg vcs "$vcs" '{
      vcs: $vcs,
      pr_url: .url,
      head_sha: .headRefOid,
      base_branch: .baseRefName,
      branch: .headRefName,
      title: .title
    }'
