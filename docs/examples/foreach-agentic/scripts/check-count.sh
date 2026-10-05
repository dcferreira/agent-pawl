#!/bin/sh
# Postcondition for ONE item: $1 is a file name under inputs/, $2 the word
# count the subagent claimed. Recounts with wc -w and exits non-zero (printing
# what it expected, which the engine feeds back on a retry) on a mismatch.
case "$1" in
  */* | .* | "") echo "refusing unsafe item name: $1" >&2; exit 2 ;;
esac
file="$(cd "$(dirname "$0")/.." && pwd)/inputs/$1"
[ -r "$file" ] || { echo "cannot read $file" >&2; exit 2; }
actual="$(wc -w < "$file" | tr -d ' ')"
if [ "$actual" != "$2" ]; then
  echo "$1: you reported $2 words but wc -w counts $actual"
  exit 1
fi
