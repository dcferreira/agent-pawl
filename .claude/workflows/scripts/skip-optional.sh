#!/usr/bin/env sh
# skip-optional.sh <declined_file> <optional_findings>
#
# Body of the `skip_optional` deterministic step, reached when ask_optional's
# answer was "skip": the user chose not to address the held minor/nitpick
# findings. Folds each one into the declined list in <declined_file> — same
# convention record-declined.sh uses for a fixer's "wont-fix", and the same
# file (see record-declined.sh for why it is a file, not a state key) — so
# a later round's cold reviewers and review_route's declined-drop don't
# re-raise the same item every round.
#
# Each item gets verify_status "wont-fix: <severity> finding skipped by the
# user" (record-declined.sh's dedup convention keys only on file/category/
# description, so this value travels with the item regardless; the
# "wont-fix:" prefix is what the decline logic keys on).
#
# The new list is the prior declined items plus the skipped optional
# findings, deduplicated on the triple (file, category, description). On a
# duplicate the LATEST occurrence wins (reverse + group_by + take-first +
# reverse, since group_by's sort is stable within equal keys) — same
# convention as record-declined.sh. Written atomically (temp file + mv);
# idempotent on a re-run.
#
# Prints {"optional_findings": []} on one line (emits: json, the default):
# the held minor/nitpick findings are consumed, so review-route.sh does not
# carry them into the next round.
set -eu

declined_file="${1:?skip-optional.sh: declined_file argument required}"
optional_findings="${2:?skip-optional.sh: optional_findings argument required}"

jq -cn --slurpfile declined "$declined_file" --argjson optional "$optional_findings" '
  ($optional | map(. + {verify_status: ("wont-fix: " + (.severity // "optional") + " finding skipped by the user")})) as $new
  | ($declined[0] + $new) as $all
  | $all
  | reverse
  | group_by([.file, .category, .description])
  | map(.[0])
  | reverse
' >"${declined_file}.tmp"
mv "${declined_file}.tmp" "$declined_file"

printf '%s\n' '{"optional_findings": []}'
