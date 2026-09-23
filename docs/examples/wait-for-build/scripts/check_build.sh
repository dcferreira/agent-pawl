#!/usr/bin/env sh
# check_build.sh <status_file>
#
# `poll:` body for the `wait_for_build` step. Reads status_file's last line;
# if it is PASSED or FAILED, prints that as the routed outcome TOKEN
# (design/format-spec.md §B.1) followed by a `pairs` payload carrying the
# same result into `build_status`:
#
#   PASSED build_status=PASSED
#   FAILED build_status=FAILED
#
# If the file doesn't exist yet or has no terminal result on its last line
# (the simulated build is still "running"), prints PENDING — a token that is
# *not* one of wait_for_build's declared `outcomes:` (PASSED, FAILED). Per
# DESIGN.md §3, `pawl poll` only ends the loop on "the first iteration whose
# last-non-empty-stdout line carries a routed token"; PENDING is a stdout
# line, logged like any other iteration, but it does not match a declared
# outcome, so `pawl poll` simply re-runs this script on the next `every:`
# tick. (Judgment call: format-spec.md §B.1 reads "TOKEN is present if and
# only if the step declares author-named outcomes:", which taken literally
# would require every poll iteration's line to carry a recognized token; but
# DESIGN.md §3 is explicit that not every iteration ends the loop, so an
# iteration with nothing to report yet has to be expressible somehow. The
# least surprising reading is that TOKEN's *presence* is required by the
# grammar whenever outcomes: exist, but the value printed need not be one of
# the *routed* names — an unrouted token is simply not acted on, exactly the
# way an unrouted line from a deterministic step would be logged and never
# parsed as anything but "not a match". PENDING is therefore always present
# as a real token, never an empty/tokenless line, so the line stays
# well-formed under the strict reading of §B.1 too.)
#
# Always exits 0: a build still in progress is not a script failure — a
# non-zero exit is unconditionally `failure` per §B.1, which would fail
# wait_for_build outright instead of letting it keep polling.
set -eu

status_file="${1:?check_build.sh: status_file argument required}"

if [ ! -f "$status_file" ]; then
  echo "PENDING"
  exit 0
fi

last_line=$(tail -n 1 -- "$status_file" 2>/dev/null || true)

case "$last_line" in
  PASSED | FAILED)
    echo "$last_line build_status=$last_line"
    ;;
  *)
    echo "PENDING"
    ;;
esac

exit 0
