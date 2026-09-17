# Install

`wf` is one static Go binary plus a Claude Code plugin. The plugin is what makes runs work inside a
session: it ships the `/wf` skill the model follows, and the two hooks that stop a run being
abandoned halfway.

## Install the plugin

```
/plugin install wf@anthropic-experimental
```

Then start a run, or force the fetch now:

```
› wf version
wf 0.4.2 (plugin pin 0.4.2)  binary: ~/.claude/wf/bin/wf
```

The first `/wf` use runs `bin/install.sh`, which downloads the pinned `wf` release into
`~/.claude/wf/bin/`. It prints one line when it does:

```
wf: fetched 0.4.2 → ~/.claude/wf/bin/wf
```

If a `wf` is already on your `PATH` and its version matches the plugin's pin, that one is used and
nothing is downloaded.

## What gets installed where

| Path | What |
|---|---|
| `~/.claude/plugins/wf/` | the plugin: `/wf` skill, `hooks/hooks.json`, `bin/install.sh` |
| `~/.claude/wf/bin/wf` | the engine binary, at the pinned version |
| `~/.claude/wf/live/` | symlinks to live runs; how the hooks find a run in ~2 ms |
| `~/.local/state/wf/` | run directories: journal, status, lock. One per run |
| `~/.claude/workflows/` | your user-level workflows (you create this) |

The hooks are bound once, at install:

```
PreToolUse (matcher: Bash) → wf-hook pre
Stop                       → wf-hook stop
```

Both are a ten-line shell fast path. With no live run they exit 0 after one directory check, so
they cost about 2 ms on every Bash call and nothing else. `wf` never installs, rewrites or removes
them at run time.

## Verify

```
› wf version
wf 0.4.2 (plugin pin 0.4.2)  binary: ~/.claude/wf/bin/wf
› wf list
No workflows found (looked in ./.claude/workflows/ and ~/.claude/workflows/).
```

The real check is the banner on the first line of any run:

```
› wf run tidy
  hooks: PreToolUse ✔  Stop ✔   guards: 0 advisory (pattern-matched)  invariants: 0
```

Two ✔ means both hooks answered the self-test. `wf run` refuses to start if either is missing —
see [troubleshooting.md](troubleshooting.md#hooks-not-live).

## Alternatives to the plugin

```
brew install wf
go install github.com/…/wf@v0.4.2
```

Both put `wf` on your `PATH`, and the plugin will use it if the version matches its pin. Neither
installs the skill or the hooks. Without the skill the model does not know the handshake; without
the hooks `wf run` refuses to start. If you cannot install plugins, the `/wf` skill's frontmatter
carries a `hooks:` block that wires the same two hooks when the skill is loaded — copy the skill
into `~/.claude/skills/wf/`.

## Updating

```
/plugin update wf
```

The plugin moves its pin; the next run fetches the matching binary. A binary whose version differs
from the pin is refused, loudly, with both numbers:

```
wf: binary 0.4.2 does not match plugin pin 0.5.0. Run `/wf` to fetch the pinned build,
    or `brew upgrade wf`.
```

This is deliberate: the skill text, the hook payload format and the binary change together.

## Uninstall

```
/plugin uninstall wf
rm -rf ~/.claude/wf ~/.local/state/wf
```

Nothing else is left behind. Run directories under `~/.local/state/wf/` are plain files — delete
one and that run is gone. Your workflow YAML lives in your repo and is untouched.

---

Next: [quickstart.md](quickstart.md).
