#!/usr/bin/env sh
# kick_off_build.sh <status_file> [force_result]
#
# Body of the `kick_off_build` deterministic step. Truncates status_file (so
# a stale result from a previous run of this example can't be mistaken for a
# fresh one), launches scripts/simulate_build.sh in the background so this
# step returns immediately rather than blocking on the ~15-20s simulated CI
# run, and prints a `{"build_started_at": "..."}` JSON object on its last
# stdout line — the payload `kick_off_build` declares no `outcomes:` for, so
# no TOKEN precedes it (design/format-spec.md §B.1); `emits: json` is the
# default so the workflow does not need to say so.
#
# force_result (PASSED|FAILED, default PASSED) is threaded straight through
# to simulate_build.sh so the workflow's `force_result` arg
# (`pawl run wait-for-build force_result=FAILED`) can exercise the FAILED
# routing without editing anything.
set -eu

status_file="${1:?kick_off_build.sh: status_file argument required}"
force_result="${2:-PASSED}"

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

mkdir -p "$(dirname -- "$status_file")"
: > "$status_file"

started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# Backgrounded and detached from this shell's stdio so kick_off_build.sh can
# return (and this step's short-lived process can exit) without the CI
# simulation getting a SIGHUP.
nohup "$script_dir/simulate_build.sh" "$status_file" "$force_result" \
  >/dev/null 2>&1 &

printf '{"build_started_at": "%s"}\n' "$started_at"
