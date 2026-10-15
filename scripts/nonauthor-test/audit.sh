#!/usr/bin/env bash
# audit.sh - grep the tester's session transcripts for non-author-test violations (protocol layer 3).
#
#   bash scripts/nonauthor-test/audit.sh <ROOT>
#
# Prints each hit as file:line plus a ~200-char excerpt, then the distinct model id(s) used.
# Exit 0: no hits. Exit 1: hits (a person must review each; a real source read invalidates the run).
# Exit 2: no transcripts found. Reading DESIGN.md / design/format-spec.md under ~/pawl-docs is
# allowed (they are in the docs snapshot), so those names are not patterns.
set -euo pipefail
[ $# -eq 1 ] || { echo "usage: audit.sh <ROOT>" >&2; exit 2; }
ROOT="$(cd "$1" && pwd -P)"
CFG="$ROOT/claude-config"
[ -d "$CFG" ] || { echo "audit.sh: $CFG missing" >&2; exit 2; }

files=()
while IFS= read -r -d '' f; do files+=("$f"); done < <(find "$CFG" -type f \( -name '*.jsonl' -o -name '*.json' -path '*/projects/*' \) -print0 2>/dev/null | sort -z)
[ "${#files[@]}" -gt 0 ] || { echo "audit.sh: no session transcripts under $CFG" >&2; exit 2; }

PATTERNS=(
  'github\.com/dcferreira/agent-pawl/(blob|tree|raw)'
  'raw\.githubusercontent'
  'api\.github\.com/repos'
  'git clone'
  'gh repo'
  'AGENTS\.md'
  'internal/'
  'cmd/pawl'
  '"name" ?: ?"WebFetch"'
  '"name" ?: ?"WebSearch"'
)
hits=0
for pat in "${PATTERNS[@]}"; do
  for f in "${files[@]}"; do
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      n="${line%%:*}"
      rest="${line#*:}"
      # excerpt centred on the first match
      ex="$(printf '%s' "$rest" | grep -oE ".{0,100}($pat).{0,100}" | head -n1 || true)"
      printf '[%s] %s:%s: %s\n' "$pat" "$f" "$n" "${ex:-$(printf '%s' "$rest" | cut -c1-200)}"
      hits=$((hits + 1))
    done < <(grep -nE -e "$pat" "$f" 2>/dev/null || true)
  done
done

echo "---"
echo "transcript files scanned: ${#files[@]}"
echo "model id(s) found:"
{ for f in "${files[@]}"; do grep -ohE '"model" ?: ?"[^"]*"' "$f" 2>/dev/null || true; done; } | sed -E 's/"model" ?: ?//' | sort | uniq -c | sort -rn | sed 's/^/  /'
if [ "$hits" -gt 0 ]; then
  echo "AUDIT: $hits hit(s): human review needed"
  exit 1
fi
echo "AUDIT: clean"
