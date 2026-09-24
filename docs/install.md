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
`submit`, `version`, `update`. That is the complete command surface of this build. In particular:

- **`pawl poll` and `pawl hook` do not exist.** There is no `wait`/`human` step kind to poll for
  (see below), and there are no hooks to invoke.
- **`pawl run` never refuses to start for lack of enforcement.** It prints
  `enforcement: off (milestone 1)` in its banner and proceeds — see
  [dogfood.md](dogfood.md) for what that means in practice.

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

There is no other installed state to remove: no plugin directory, no `~/.claude/pawl/`, no global
`~/.local/state/pawl/`. Where run state actually lives is `internal/journal`'s run directory — see
`docs/running.md` and `pawl status`'s `root:` line for the mechanism that exists today.

## Step kinds and validator scope in this build

All five step kinds are implemented: `deterministic`, `agentic`, `wait`, `human`, and `parallel`
(single-group, all-or-nothing `branches:` join — [design/format-spec.md](../design/format-spec.md)
§B.15). Top-level `guards:`, `invariants:`, and a step's `retry:` field remain unimplemented:
declaring any of them is a validation error, not a quietly-ignored field.

---

Next: [dogfood.md](dogfood.md) — actually running a workflow end to end.
