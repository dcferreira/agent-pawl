#!/bin/sh
# Discover the work list at runtime: every inputs/*.txt next to this
# workflow, as a json array of file names. Emits {"items": [...]} for the
# discover step's `writes: [items]`.
dir="$(cd "$(dirname "$0")/.." && pwd)/inputs"
out=""
for f in "$dir"/*.txt; do
  [ -e "$f" ] || continue
  name="$(basename "$f")"
  out="${out:+$out,}\"$name\""
done
printf '{"items":[%s]}\n' "$out"
