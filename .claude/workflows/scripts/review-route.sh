#!/usr/bin/env sh
# review-route.sh <findings_claude> <findings_codex> <findings_custom>
#
# Body of the `review_route` deterministic step: merges the three parallel
# branches' findings arrays (each defaults to `[]` when that engine wasn't
# selected — see the branch steps' own descriptions) and decides whether
# another fix round is needed.
#
# Prints "<TOKEN> {\"findings\": [...]}" — a routed TOKEN (blocking/clean)
# followed by a JSON payload (emits: json, the default). Using JSON here
# rather than `pairs` for the same reason as fetch-pr.sh: finding
# descriptions routinely contain spaces, which the pairs grammar can't
# carry safely.
set -eu

findings_claude="${1:?review-route.sh: findings_claude argument required}"
findings_codex="${2:?review-route.sh: findings_codex argument required}"
findings_custom="${3:?review-route.sh: findings_custom argument required}"

merged=$(jq -cn \
  --argjson a "$findings_claude" \
  --argjson b "$findings_codex" \
  --argjson c "$findings_custom" \
  '$a + $b + $c')

if [ "$(printf '%s' "$merged" | jq 'length')" -gt 0 ]; then
  printf 'blocking {"findings": %s}\n' "$merged"
else
  printf 'clean {"findings": %s}\n' "$merged"
fi
