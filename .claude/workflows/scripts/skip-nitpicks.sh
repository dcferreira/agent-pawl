#!/usr/bin/env sh
# skip-nitpicks.sh <declined> <nitpicks>
#
# Body of the `skip_nitpicks` deterministic step, reached when ask_nitpicks'
# answer was "skip": the user chose not to address this round's nitpicks.
# Folds each nitpick into `declined` — same convention record-declined.sh
# uses for a fixer's "wont-fix" — so a later round's cold reviewers (told
# ${declined}) and review_route's declined-drop don't re-raise the same
# nitpick every round.
#
# Each nitpick gets verify_status "wont-fix: nitpick skipped by the user"
# (record-declined.sh's dedup convention keys only on file/category/
# description, so this value travels with the item regardless).
#
# The new list is the prior declined items plus this round's skipped
# nitpicks, deduplicated on the triple (file, category, description). On a
# duplicate the LATEST occurrence wins (reverse + group_by + take-first +
# reverse, since group_by's sort is stable within equal keys) — same
# convention as record-declined.sh.
#
# Prints {"declined": [...]} on one line (emits: json, the default; jq -c
# keeps it on the one line the engine parses, format-spec §B.1).
set -eu

declined="${1:?skip-nitpicks.sh: declined argument required}"
nitpicks="${2:?skip-nitpicks.sh: nitpicks argument required}"

jq -cn --argjson declined "$declined" --argjson nitpicks "$nitpicks" '
  ($nitpicks | map(. + {verify_status: "wont-fix: nitpick skipped by the user"})) as $new
  | ($declined + $new) as $all
  | {
      declined: (
        $all
        | reverse
        | group_by([.file, .category, .description])
        | map(.[0])
        | reverse
      )
    }
'
