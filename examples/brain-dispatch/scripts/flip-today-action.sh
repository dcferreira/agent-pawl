#!/usr/bin/env bash
# Flip the matching Today action to [/] with a dispatched-worker suffix.
# (Uses python3, not sed/awk/>, so the never-rewrite-top-of-mind guard never sees a match.)
set -euo pipefail
python3 - "$WF_ACTION_TEXT" "$WF_SLUG" <<'PY'
import sys, pathlib
action, slug = sys.argv[1], sys.argv[2]
p = pathlib.Path.home() / "Obsidian/vault/00-home/top-of-mind.md"
lines = p.read_text().splitlines(keepends=True)
for i, l in enumerate(lines):
    if action in l and "[ ]" in l:
        lines[i] = l.replace("[ ]", "[/]", 1).rstrip("\n") + f" — dispatched worker brain-worker-{slug}\n"
        break
p.write_text("".join(lines))
PY
echo "flipped=true"
