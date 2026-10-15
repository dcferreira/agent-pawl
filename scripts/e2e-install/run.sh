#!/usr/bin/env bash
# run.sh [a b c e f ...] - run install/update verification scenarios.
# Default: every scenario-<x>.sh that exists. Manual use, not CI. See README.md.
# Env: FROM=0.7.0 TO=0.8.0 SOURCE=live|local REPORT_DIR KEEP=1 E2E_TMP
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FROM="${FROM:-0.7.0}"
TO="${TO:-0.8.0}"
SOURCE="${SOURCE:-live}"
E2E_TMP="${E2E_TMP:-${TMPDIR:-/tmp}}"
export FROM TO SOURCE E2E_TMP
if [ -z "${REPORT_DIR:-}" ]; then
  mkdir -p "$E2E_TMP"
  REPORT_DIR="$(mktemp -d "$E2E_TMP/pawl-e2e-report.XXXXXX")"
fi
export REPORT_DIR
echo "REPORT_DIR=$REPORT_DIR"

if [ $# -gt 0 ]; then
  scenarios=("$@")
else
  scenarios=()
  for f in "$HERE"/scenario-*.sh; do
    [ -e "$f" ] || continue
    s="${f##*/scenario-}"
    scenarios+=("${s%.sh}")
  done
fi

failed=0
for s in "${scenarios[@]}"; do
  f="$HERE/scenario-$s.sh"
  if [ ! -f "$f" ]; then
    echo "no such scenario: $s" >&2
    failed=1
    continue
  fi
  echo "== scenario $s (FROM=$FROM TO=$TO SOURCE=$SOURCE) =="
  if ! bash "$f"; then
    echo "scenario $s: FAILED"
    failed=1
  fi
done

echo
echo "== summary ($REPORT_DIR/summary.md) =="
cat "$REPORT_DIR/summary.md" 2>/dev/null || echo "(no checks recorded)"
exit "$failed"
