#!/usr/bin/env bash
# Mark the worker note status: working; fix the attach: line if stale.
set -euo pipefail
note=~/Obsidian/vault/workers/${PAWL_SLUG}.md
sed -i 's/^status: .*/status: working/' "$note"
if grep -q '^attach:' "$note"; then
  sed -i "s|^attach:.*|attach: ${PAWL_ATTACH}|" "$note"
else
  echo "attach: ${PAWL_ATTACH}" >> "$note"
fi
