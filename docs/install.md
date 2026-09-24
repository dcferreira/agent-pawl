# Install

**`install.sh` and tagged release binaries exist as of this build** (`.goreleaser.yaml`,
`.github/workflows/release.yml`), and tagged releases are published on GitHub for `install.sh` to
fetch. DESIGN.md §9 describes a fuller Claude Code plugin story (self-installing a pinned release
binary via two static hooks) as the intended end state; that hook-based auto-install is still not
built — what exists is the plain `install.sh` / GitHub Releases pair below, plus a Claude Code
plugin (see the README's Installation section) that ships the `/agent-pawl:pawl` skill — the
plugin does not and cannot ship the `pawl` binary itself, so you still install it separately, with
either `install.sh` or `go install`.

## Install via install.sh

```
curl -fsSL https://raw.githubusercontent.com/dcferreira/agent-pawl/main/install.sh | sh
```

This detects your OS (linux/darwin) and architecture (amd64/arm64), downloads the matching
`pawl_<version>_<os>_<arch>.tar.gz` and `checksums.txt` from the latest GitHub Release, verifies
the archive's sha256 against `checksums.txt` before extracting anything, and installs `pawl` to
`$HOME/.local/bin` (override with `INSTALL_DIR=...`). It prints a `PATH` reminder if that
directory isn't already on your `PATH`.

Env vars:

- `PAWL_VERSION` — install a specific version (e.g. `v0.1.0` or `0.1.0`) instead of latest.
- `INSTALL_DIR` — install location, default `$HOME/.local/bin`.

It has no dependency on the Go toolchain — only `curl` or `wget`, `tar`, and `sha256sum` or
`shasum` (whichever your OS ships). It fails with an explicit, actionable message on any OS other
than Linux/macOS or any architecture other than amd64/arm64 (e.g. Windows, 32-bit x86), rather
than silently doing the wrong thing.

The pure parts of `install.sh` (OS/arch detection, asset naming, version resolution, checksum-line
parsing) are unit-tested without touching the network in `scripts/test-install.sh` — run via
`make test-install`.

### Upgrading a binary installed this way

Once you have a release binary installed (via `install.sh` above, or otherwise), `pawl update`
upgrades it in place — same checksum verification as `install.sh`, no need to re-run the curl
one-liner:

```
pawl update
```

See [cli.md](cli.md) for `--check`, `--version` (pin/rollback) and `--force`. A
binary built from source (`pawl version` prints `pawl dev`) is a different case — see the note
right below.

## Build and install the binary

You need Go (this build was developed and tested against Go 1.27) and a clone of this repo.

```
make install
```

This is exactly `go install ./cmd/pawl` (see the `Makefile`). It builds `cmd/pawl` and drops `pawl` at
`$(go env GOPATH)/bin/pawl` — make sure that directory is on your `PATH`. Equivalently, if you don't
want to clone the repo yourself, install straight from the published module:

```
go install github.com/dcferreira/agent-pawl/cmd/pawl@latest
```

`@latest` resolves to the latest tagged release (pin one with `@vX.Y.Z` instead), but it's still a
source build, so `pawl version` still prints `pawl dev` rather than the tag — use `install.sh` if
you want a stamped release binary.

If you'd rather not touch `$GOPATH/bin`, `make build` puts the binary at `./dist/pawl` in the repo
instead (not `./bin/`, which is a committed plugin directory — see the plugin section below):

```
make build
./dist/pawl version
```

## Verify

```
pawl version
```

prints `pawl dev` for anything built from source with plain `go build`/`go install`/`make
install`/`make build`, because those don't set the `-ldflags "-X main.Version=..."` that
`cmd/pawl/main.go` supports — `pawl dev` is what building from source correctly looks like, not a
symptom of a bad build. A binary installed via `install.sh` prints the released version instead
(e.g. `pawl 0.1.0`, no leading `v` — `.goreleaser.yaml` sets that ldflag to goreleaser's
`{{.Version}}` template value, which is the tag with its `v` stripped, not the raw git tag).

A `pawl dev` (source) build is exactly what `pawl update` refuses to touch without `--force` — see
[cli.md](cli.md) — since overwriting a build you made yourself with a
downloaded release binary is not something `pawl update` should ever do by default.

```
pawl
```

with no arguments prints the command list — `run`, `validate`, `status`, `abandon`, `list`,
`submit`, `poll`, `hook`, `version`, `update`. That is the complete command surface of this build. In particular:

- **`pawl hook pre|stop`** is the enforcement hooks' entry point — see [#hooks](#hooks) below.
- **`pawl run` refuses to start (exit 4) without a live `PreToolUse` heartbeat**, unless you pass
  `--no-enforcement` or set `PAWL_ENFORCEMENT=off` — see [#hooks](#hooks) and
  [dogfood.md](dogfood.md) for what that means in practice.

## Hooks

`pawl run` won't start unless `pawl`'s `PreToolUse` hook has fired for this working copy within the last
5 minutes — its way of confirming the enforcement hooks are actually wired up, since it has no
other way to ask Claude Code that directly. Without a fresh heartbeat, `pawl run` refuses:

```
pawl: refusing to start: pawl's PreToolUse hook has not fired for this working copy in the last 5 minutes.
Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.
```

Allowlisting `Bash(pawl:*)` in Claude Code's permissions avoids an approval prompt on every `pawl`
call — worth doing on its own, and it also means the heartbeat gets written without you having to
wait on a prompt first.

**The Claude Code plugin wires the hooks automatically.** Once installed (see the README's
Installation section), the plugin's `hooks/hooks.json` binds `PreToolUse` (matcher `Bash`) and `Stop`
to `${CLAUDE_PLUGIN_ROOT}/bin/pawl-hook pre|stop` — a fast-path wrapper that hands the payload to
`pawl hook pre|stop` once anything is actually live, or once the payload itself mentions `pawl` (so a
fresh heartbeat is written even before any run is live). Nothing further to configure; restart the
session after installing so the hooks bind.

**Without the plugin**, add the same two hooks to your own `settings.json` directly — there is no
`pawl-hook` on your `PATH` to fast-path through, so these call `pawl hook pre`/`pawl hook stop`
straight:

```json
{
  "hooks": {
    "PreToolUse": [{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "pawl hook pre" }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "pawl hook stop" }] }]
  }
}
```

**Opting out**: pass `--no-enforcement` to `pawl run`, or set `PAWL_ENFORCEMENT=off` — the banner
says so (`enforcement: off (--no-enforcement)` / `enforcement: off (PAWL_ENFORCEMENT=off)`), and any
`guards:` the workflow declares are reported as declared but not enforced. This is a real, visible
opt-out, not a workaround: nothing is checked for that run. The opt-out is recorded in the run's
journal at start and holds for the run's whole life — the hooks ignore it (no guards, no subagent VCS
rule, no `Stop` refusal) even on resume, submit or poll. It works the other way too: an opted-out
run resumes without a heartbeat, and an enforced run can't be opted out on resume (`pawl run`
refuses `--no-enforcement` there). An enforced run doesn't need a heartbeat to resume either: you can
resume it from a plain terminal, and it stays enforced. See [cli.md](cli.md).

Once hooks are on, `Stop` will refuse to end the driving session's turn while its run is at an
`agentic`/`parallel` step awaiting `pawl submit` — finish the dispatch, or run
`pawl abandon --run <id>` to release it. See [cli.md](cli.md) and
[troubleshooting.md](troubleshooting.md) for the exact messages.

## What `make check` runs

```
make check
```

runs `go fmt ./...`, `go vet ./...`, `go test ./...`, and `scripts/test-install.sh` (install.sh's
unit tests). This is the same check a change to this repo is expected to pass; running it after
`make install` is a reasonable sanity check that your Go toolchain and checkout are in order,
though it is not required just to use the binary.

## Uninstall

```
rm $(go env GOPATH)/bin/pawl
```

or, if installed via `install.sh`:

```
rm "${INSTALL_DIR:-$HOME/.local/bin}/pawl"
```

There is no other installed state from the binary install itself to remove: no plugin directory, no
global `~/.local/state/pawl/`. `~/.claude/pawl/` does now hold state — `runs/`, `live/`, and
`heartbeat/`, written by the enforcement hooks — but that's plugin/hook state, not something this
binary uninstall touches; remove it by hand if you also want that gone. Where run state actually
lives is `internal/journal`'s run directory — see `docs/running.md` and `pawl status`'s `root:` line
for the mechanism that exists today.

## Step kinds and validator scope in this build

All five step kinds are implemented: `deterministic`, `agentic`, `wait`, `human`, and `parallel`
(single-group, all-or-nothing `branches:` join — [design/format-spec.md](../design/format-spec.md)
§B.15). Top-level `guards:` is now parsed and validated (id required+unique, `match:` required,
RE2-compilable, and not able to match zero characters; `only_in:` required — see
[validation.md](validation.md) rule 15) and enforced by the `PreToolUse` hook, advisory and
pattern-matched — see [#hooks](#hooks) above. Top-level `invariants:` IS engine-checked: the engine
evaluates every declared invariant after every step completion and after every `pawl
submit`/`pawl poll` call, and a violation blocks the run — `pawl run`'s banner prints an
`invariants: N (engine-checked after every step)` line whenever a workflow declares any. A step's
`retry:` field (deterministic and wait only) is implemented — see §B.16.

---

Next: [dogfood.md](dogfood.md) — actually running a workflow end to end.
