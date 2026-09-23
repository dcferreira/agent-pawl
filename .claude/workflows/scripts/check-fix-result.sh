#!/usr/bin/env sh
# check-fix-result.sh <vcs> <findings> <fix_input_file>
#
# fix_issues' postcondition. fix_issues is an agentic step: nothing here
# stops the fixer from claiming things that aren't true, so this
# independently re-checks its return against reality instead of trusting
# it (format-spec.md §B.8's "postconditions that re-observe reality"):
#
#   (i)   every returned item's verify_status matches the declared prefix
#         grammar (^(fixed|partially-fixed|not-fixed|wont-fix):) — the
#         original check;
#   (ii)  every INPUT item (matched on the (file, category, description)
#         triple review-route.sh/record-declined.sh already key on) is
#         still present in the OUTPUT — a fixer that returns fewer items,
#         or `[]`, cannot silently make findings disappear;
#   (iii) if the working copy has no uncommitted changes, no item may
#         claim `fixed:`/`partially-fixed:` — on jj that's an empty
#         `jj diff -r @ --summary`, on git an empty `git status
#         --porcelain`. Those claims require a change that didn't happen;
#         the fixer must say `wont-fix: already resolved` (or another
#         wont-fix reason) or `not-fixed:` instead. This is what makes
#         unchanged_route safe to treat "declines only" as "nothing left
#         unresolved" (see unchanged-route.sh).
#
# <findings> is fix_issues' returned array, taken as an ordinary ${key}
# argument like the postcondition it replaces (fix_issues' own output,
# already bounded by the same 128 KiB argv limit every other consumer of
# `findings` lives with — ci-failures.sh keeps it under a documented
# budget). <fix_input_file> is NOT re-passed as an argument: it is the
# path (state key `fix_input_file`, created by fetch-pr.sh, kept current by
# whichever step sets `findings` right before fix_issues — review-route.sh,
# take-optional.sh, ci-failures.sh) to a file holding a snapshot of exactly
# what fix_issues was asked to fix. Putting a second full copy of the same
# findings on this command's argv (input AND output) could overflow the
# 128 KiB argv/env cap on its own — CI findings can already be up to ~64 KiB
# (ci-failures.sh's PAWL_CI_INLINE_BUDGET) — so the input side is read from
# a file instead, the same reason declined_file/record-declined.sh use one.
#
# On failure, prints a human-readable reason to STDOUT (the engine parses a
# postcondition's failure text from stdout first, falling back to stderr
# only if stdout was empty — internal/engine/postcondition.go) naming the
# offending items, since that text is what the next of fix_issues' 3
# attempts sees.
set -eu

vcs="${1:?check-fix-result.sh: vcs argument required}"
findings="${2:?check-fix-result.sh: findings argument required}"
fix_input_file="${3:?check-fix-result.sh: fix_input_file argument required}"

if [ ! -e "$fix_input_file" ]; then
  echo "check-fix-result.sh: snapshot file ${fix_input_file} does not exist (fix_issues' preceding step should have written it)" >&2
  exit 1
fi

fail=0
reasons=""

add_reason() {
  reasons="${reasons}${reasons:+$(printf '\n')}$1"
}

if ! printf '%s' "$findings" | jq -e 'type == "array"' >/dev/null 2>&1; then
  echo "check-fix-result.sh: fix_issues returned findings that are not a JSON array"
  exit 1
fi

# (i) verify_status prefix grammar.
bad_prefix=$(printf '%s' "$findings" | jq -r '
  [.[] | select(((.verify_status // "") | test("^(fixed|partially-fixed|not-fixed|wont-fix):")) | not)]
  | map((.file // "?") + "/" + (.category // "?") + ": " + (.description // "?") + " (verify_status: " + (.verify_status // "<missing>") + ")")
  | .[]')
if [ -n "$bad_prefix" ]; then
  fail=1
  add_reason "$(printf 'item(s) with a verify_status that does not start with fixed:/partially-fixed:/not-fixed:/wont-fix: :\n%s' "$bad_prefix")"
fi

# (ii) every input item is still present in the output, matched on
# (file, category, description).
dropped=$(jq -r -n --slurpfile input "$fix_input_file" --argjson output "$findings" '
  def key: [.file, .category, .description];
  ($output | map(key)) as $out_keys
  | ($input[0] // []) | map(select((key as $k | $out_keys | any(. == $k)) | not))
  | map((.file // "?") + "/" + (.category // "?") + ": " + (.description // "?"))
  | .[]')
if [ -n "$dropped" ]; then
  fail=1
  add_reason "$(printf 'input item(s) missing from the returned findings (nothing may be dropped, even to []):\n%s' "$dropped")"
fi

# (iii) an unchanged tree cannot have any fixed:/partially-fixed: item.
case "$vcs" in
  jj) dirty=$(jj diff -r @ --summary) ;;
  git) dirty=$(git status --porcelain) ;;
  *)
    echo "check-fix-result.sh: unknown vcs '${vcs}' (expected jj or git)" >&2
    exit 1
    ;;
esac

if [ -z "$dirty" ]; then
  false_claims=$(printf '%s' "$findings" | jq -r '
    [.[] | select((.verify_status // "") | test("^(fixed|partially-fixed):"))]
    | map((.file // "?") + "/" + (.category // "?") + ": " + (.description // "?") + " (verify_status: " + .verify_status + ")")
    | .[]')
  if [ -n "$false_claims" ]; then
    fail=1
    add_reason "$(printf 'the working copy has NO uncommitted changes, so these item(s) cannot be fixed:/partially-fixed: (use wont-fix: already resolved, another wont-fix reason, or not-fixed: instead):\n%s' "$false_claims")"
  fi
fi

if [ "$fail" -ne 0 ]; then
  printf '%s\n' "$reasons"
  exit 1
fi
