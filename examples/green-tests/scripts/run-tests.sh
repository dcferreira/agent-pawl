#!/usr/bin/env bash
# run-tests.sh <test_cmd> — runs the given command line (received as one
# shell-quoted token, since ${test_cmd} substitutes into `run:` that way —
# design/format-spec.md §B.2) and prints the token+payload line run_tests
# (kind: deterministic, emits: json) expects:
#
#   PASS                                on exit 0
#   FAIL {"failures": "<jq -Rs .>"}      on any non-zero exit
#
# The failure text is the command's combined stdout+stderr, JSON-encoded via
# `jq -Rs .` so a multi-line, whitespace-laden go test failure survives
# intact as the `failures` state value (Ruling R11 — this is why the example
# uses `emits: json` and not `pairs`). Requires `jq`.
#
# This script always exits 0: a failing suite is a named outcome (FAIL),
# not a run_tests engine failure — a non-zero exit here would mean
# "run_tests itself is broken", which is a different thing entirely.
set -u

if [ $# -lt 1 ]; then
  echo 'FAIL {"failures": "run-tests.sh: missing test_cmd argument"}'
  exit 0
fi

if ! command -v jq >/dev/null; then
  echo 'FAIL {"failures": "run-tests.sh requires jq"}'
  exit 0
fi

output=$(eval "$1" 2>&1)
status=$?

if [ "$status" -eq 0 ]; then
  echo "PASS"
else
  failures=$(printf '%s' "$output" | jq -Rs .)
  echo "FAIL {\"failures\": $failures}"
fi

exit 0
