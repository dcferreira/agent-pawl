#!/bin/sh
# Check ONE item: $1 is a file name under inputs/, $2 a marker that must not
# appear in it. Exits non-zero (a hard failure of this item only) when the
# marker is present; otherwise reports a verdict for the body's `writes:`.
case "$1" in
  */* | .* | "") echo "refusing unsafe item name: $1" >&2; exit 2 ;;
esac
file="$(cd "$(dirname "$0")/.." && pwd)/inputs/$1"
if grep -q -- "$2" "$file"; then
  echo "$1 contains forbidden marker $2" >&2
  exit 1
fi
printf '{"verdict":"clean"}\n'
