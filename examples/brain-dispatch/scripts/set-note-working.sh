#!/usr/bin/env bash
# Mark the worker note status: working; fix the attach: line if stale.
set -euo pipefail
note=~/Obsidian/vault/workers/${WF_SLUG}.md
sed -i 's/^status: .*/status: working/' "$note"
if grep -q '^attach:' "$note"; then
  sed -i "s|^attach:.*|attach: ${WF_ATTACH}|" "$note"
else
  echo "attach: ${WF_ATTACH}" >> "$note"
fi
