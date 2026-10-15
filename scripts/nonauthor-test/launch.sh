#!/usr/bin/env bash
# launch.sh - start the sandboxed tester in a detached tmux session brain-worker-<slug>.
#
#   bash scripts/nonauthor-test/launch.sh <ROOT> <slug>            # run the brief (default)
#   bash scripts/nonauthor-test/launch.sh <ROOT> <slug> --login    # one-time human login, no brief
#   bash scripts/nonauthor-test/launch.sh <ROOT> <slug> --resume   # environment recovery only
#
# The tester uses the claude CLI's default model (no --model), as a stranger would. This script types
# exactly one command line into the pane and nothing else. Never `send-keys` anything further by
# hand: that would be coaching (see PROTOCOL.md).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ $# -lt 2 ] || [ $# -gt 3 ]; then
  echo "usage: launch.sh <ROOT> <slug> [--login|--resume]" >&2
  exit 2
fi
ROOT="$(cd "$1" && pwd -P)"
SLUG="$2"
MODE="${3:-run}"
case "$SLUG" in "" | *[!A-Za-z0-9._-]*) echo "launch.sh: bad slug '$SLUG'" >&2; exit 2 ;; esac
case "$MODE" in run | --login | --resume) ;; *) echo "launch.sh: unknown option '$MODE'" >&2; exit 2 ;; esac
[ -f "$ROOT/kit.env" ] || { echo "launch.sh: $ROOT/kit.env missing" >&2; exit 2; }
SESSION="brain-worker-$SLUG"
# The user's shell aliases tmux; `command` bypasses it.
if command tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "launch.sh: tmux session $SESSION already exists; refusing" >&2
  exit 1
fi

command tmux new-session -d -s "$SESSION" -x 220 -y 50 "$HERE/enter.sh $ROOT"

# Wait for the sandbox shell prompt (PS1 is '\u@sandbox:\w$ ').
up=no
for _ in $(seq 1 30); do
  if command tmux capture-pane -p -t "$SESSION" 2>/dev/null | grep -q '@sandbox:'; then up=yes; break; fi
  sleep 1
done
if [ "$up" != yes ]; then
  echo "launch.sh: sandbox shell prompt never appeared in $SESSION" >&2
  command tmux capture-pane -p -t "$SESSION" 2>/dev/null | tail -n 20 >&2 || true
  exit 1
fi

case "$MODE" in
  run)
    line='claude --dangerously-skip-permissions "Read ~/BRIEF.md and do the task it describes."'
    ;;
  --resume)
    line='claude --continue --dangerously-skip-permissions'
    ;;
  --login)
    line='claude'
    ;;
esac
command tmux send-keys -t "$SESSION" -l "$line"
command tmux send-keys -t "$SESSION" Enter

if [ "$MODE" = --login ]; then
  cat <<MSG
Started sandboxed claude for login in tmux session $SESSION (login not verified here).
For the human:
  1. tmux attach -t $SESSION
  2. complete onboarding and /login (credentials are written only under $ROOT)
  3. type /exit, then detach (Ctrl-b d) and kill the session: tmux kill-session -t $SESSION
Then run verify-isolation.sh and launch.sh again without --login.
MSG
  exit 0
fi

# Poll for Claude's composer (up to ~60s). Heuristic: the TUI's input box / prompt marker.
verified=no
for _ in $(seq 1 30); do
  pane="$(command tmux capture-pane -p -t "$SESSION" 2>/dev/null || true)"
  # The one-time bypass-permissions dialog also draws a box and a ❯ selector: never count it.
  if printf '%s\n' "$pane" | grep -qE 'Yes, I accept|No, exit'; then
    sleep 2
    continue
  fi
  if printf '%s\n' "$pane" | grep -qE '^[[:space:]]*(>|❯)|╭─|│ >|shift\+tab|\? for shortcuts|bypass permissions on'; then
    verified=yes
    break
  fi
  sleep 2
done
echo "verified=$verified session=$SESSION attach=tmux attach -t $SESSION"
