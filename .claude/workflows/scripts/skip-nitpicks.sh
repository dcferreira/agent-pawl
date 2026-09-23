#!/usr/bin/env sh
# skip-nitpicks.sh <declined_file> <nitpicks>
#
# Body of the `skip_nitpicks` deterministic step, reached when ask_nitpicks'
# answer was "skip": the user chose not to address the held nitpicks.
# Folds each nitpick into the declined list in <declined_file> — same
# convention record-declined.sh uses for a fixer's "wont-fix", and the same
# file (see record-declined.sh for why it is a file, not a state key) — so
# a later round's cold reviewers and review_route's declined-drop don't
# re-raise the same nitpick every round.
#
# Each nitpick gets verify_status "wont-fix: nitpick skipped by the user"
# (record-declined.sh's dedup convention keys only on file/category/
# description, so this value travels with the item regardless).
#
# The new list is the prior declined items plus the skipped nitpicks,
# deduplicated on the triple (file, category, description). On a duplicate
# the LATEST occurrence wins (reverse + group_by + take-first + reverse,
# since group_by's sort is stable within equal keys) — same convention as
# record-declined.sh. Written atomically (temp file + mv); idempotent on a
# re-run.
#
# Prints {"nitpicks": []} on one line (emits: json, the default): the held
# nitpicks are consumed, so review-route.sh does not carry them into the
# next round.
set -eu

declined_file="${1:?skip-nitpicks.sh: declined_file argument required}"
nitpicks="${2:?skip-nitpicks.sh: nitpicks argument required}"

jq -cn --slurpfile declined "$declined_file" --argjson nitpicks "$nitpicks" '
  ($nitpicks | map(. + {verify_status: "wont-fix: nitpick skipped by the user"})) as $new
  | ($declined[0] + $new) as $all
  | $all
  | reverse
  | group_by([.file, .category, .description])
  | map(.[0])
  | reverse
' >"${declined_file}.tmp"
mv "${declined_file}.tmp" "$declined_file"

printf '%s\n' '{"nitpicks": []}'
