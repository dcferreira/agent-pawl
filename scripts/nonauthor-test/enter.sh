#!/usr/bin/env bash
# enter.sh - run a command (default: an interactive login shell) inside the non-author sandbox.
#
#   bash scripts/nonauthor-test/enter.sh <ROOT> [cmd args...]
#
# bubblewrap layout (see README.md, "Safety model"): the host filesystem read-only, /home /root /tmp
# /var/tmp /run /mnt /media replaced by empty tmpfs (so the real home, other users' or processes'
# temp files, the tmux socket and the ssh/gpg agents vanish), ROOT bound back at the SAME absolute path (Claude Code's
# installed_plugins.json records absolute paths), a fresh /proc in its own pid namespace, and an
# explicit environment (--clearenv). Network stays on. Deliberately no --new-session: that breaks the
# interactive tty claude needs.
set -euo pipefail

[ $# -ge 1 ] || { echo "usage: enter.sh <ROOT> [cmd args...]" >&2; exit 2; }
ROOT="$(cd "$1" && pwd -P)" || { echo "enter.sh: no such ROOT: $1" >&2; exit 2; }
shift
[ -f "$ROOT/kit.env" ] || { echo "enter.sh: $ROOT/kit.env missing (not a prep.sh sandbox)" >&2; exit 2; }
[ -f "$ROOT.host.env" ] || { echo "enter.sh: $ROOT.host.env missing" >&2; exit 2; }
# shellcheck source=/dev/null
. "$ROOT/kit.env"
# shellcheck source=/dev/null
. "$ROOT.host.env"
[ -x "$CLAUDE_BIN" ] || { echo "enter.sh: claude binary $CLAUDE_BIN not executable" >&2; exit 2; }
[ -e "$ROOT/bin/claude" ] || { echo "enter.sh: $ROOT/bin/claude placeholder missing" >&2; exit 2; }

args=(--ro-bind / /)
for d in /home /root /tmp /var/tmp /run /mnt /media; do
  [ -d "$d" ] && args+=(--tmpfs "$d")
done
[ -d /run/systemd/resolve ] && args+=(--ro-bind /run/systemd/resolve /run/systemd/resolve)
args+=(--bind "$ROOT" "$ROOT")
# The real claude binary lives under the real HOME (hidden by the tmpfs above): bind it onto the
# empty placeholder file prep.sh left at ROOT/bin/claude.
args+=(--ro-bind "$CLAUDE_BIN" "$ROOT/bin/claude")
args+=(--dev /dev --proc /proc --unshare-pid --unshare-ipc --die-with-parent --clearenv)

path="$ROOT/bin"
[ -n "${GO_DIR:-}" ] && path="$path:$GO_DIR"
path="$path:/usr/bin:/bin"
args+=(
  --setenv HOME "$SB_HOME"
  --setenv CLAUDE_CONFIG_DIR "$SB_CFG"
  --setenv GOPATH "$SB_GOPATH"
  --setenv GOMODCACHE "$SB_GOPATH/pkg/mod"
  --setenv GOCACHE "$ROOT/gocache"
  --setenv GOFLAGS -modcacherw
  --setenv PATH "$path"
  --setenv TERM "${TERM:-xterm-256color}"
  --setenv LANG C.UTF-8
  --setenv DISABLE_AUTOUPDATER 1
  --setenv GOTELEMETRY off
  --chdir "$SB_HOME"
)

if [ $# -eq 0 ]; then
  set -- bash --login
fi
exec bwrap "${args[@]}" "$@"
