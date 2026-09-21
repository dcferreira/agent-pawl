#!/usr/bin/env sh
# simulate_build.sh <status_file> [result]
#
# Stands in for an async CI run: sleeps ~17s (roughly the middle of the
# "15-20 seconds" this example targets — kept fixed, not randomized, so the
# example stays deterministic and easy to reason about in tests) and then
# appends a single line, PASSED or FAILED, to status_file. Meant to be
# launched backgrounded (`&`, via nohup) by kick_off_build.sh so the
# deterministic step that starts it returns immediately.
#
# result (PASSED|FAILED, default PASSED) is normally threaded through from
# the workflow's `force_result` arg by kick_off_build.sh; any other value
# falls back to PASSED.
set -eu

status_file="${1:?simulate_build.sh: status_file argument required}"
result="${2:-PASSED}"

case "$result" in
  PASSED | FAILED) ;;
  *) result=PASSED ;;
esac

sleep 17

mkdir -p "$(dirname -- "$status_file")"
echo "$result" >> "$status_file"
