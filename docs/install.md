# Install

**This build has no plugin, no `install.sh`, and no release binaries.** DESIGN.md §9 describes a
Claude Code plugin (a `/pawl` skill plus two static hooks, self-installing a pinned release binary)
as the intended distribution story. None of that exists yet. What exists is a Go module you build
yourself, and a skill file you copy into place by hand — see [dogfood.md](dogfood.md).

## Build and install the binary

You need Go (this build was developed and tested against Go 1.27) and a clone of this repo.

```
make install
```

This is exactly `go install ./cmd/pawl` (see the `Makefile`). It builds `cmd/pawl` and drops `pawl` at
`$(go env GOPATH)/bin/pawl` — make sure that directory is on your `PATH`. Equivalent, if you don't
want to clone the repo yourself and it's published somewhere your `go install` can reach:

```
go install github.com/dcferreira/agent-pawl/cmd/pawl@latest
```

There is no `go install ./cmd/pawl@latest`-with-version story: nothing here is tagged or released,
so `@latest` means "whatever is on the default branch," not a pinned build.

If you'd rather not touch `$GOPATH/bin`, `make build` puts the binary at `./bin/pawl` in the repo
instead:

```
make build
./bin/pawl version
```

## Verify

```
pawl version
```

prints `pawl dev` — every build from source prints `dev`, because nothing in this build sets the
`-ldflags "-X main.Version=..."` that `cmd/pawl/main.go` supports; there is no version-numbering or
release process yet, so `pawl dev` is what installing correctly looks like, not a symptom of a bad
build.

```
pawl
```

with no arguments prints the command list — `run`, `validate`, `status`, `abandon`, `list`,
`submit`, `version`. That is the complete command surface of this build. In particular:

- **`pawl poll` and `pawl hook` do not exist.** There is no `wait`/`human` step kind to poll for
  (see below), and there are no hooks to invoke.
- **`pawl run` never refuses to start for lack of enforcement.** It prints
  `enforcement: off (milestone 1)` in its banner and proceeds — see
  [dogfood.md](dogfood.md) for what that means in practice.

## What `make check` runs

```
make check
```

runs `go fmt ./...`, `go vet ./...`, then `go test ./...`. This is the same check a change to this
repo is expected to pass; running it after `make install` is a reasonable sanity check that your
Go toolchain and checkout are in order, though it is not required just to use the binary.

## Uninstall

```
rm $(go env GOPATH)/bin/pawl
```

There is no other installed state to remove: no plugin directory, no `~/.claude/pawl/`, no global
`~/.local/state/pawl/`. Where run state actually lives is `internal/journal`'s run directory — see
`docs/running.md` and `pawl status`'s `root:` line for the mechanism that exists today.

## Step kinds and validator scope in this build

Only two step kinds are implemented: `deterministic` and `agentic`. A workflow that declares
`kind: wait`, `kind: human`, or `kind: parallel` is rejected by both `pawl validate` and `pawl run`
with a "not implemented in this build" (or, for `parallel`, "reserved for Milestone 3") message —
it does not silently no-op. The same is true of top-level `guards:`, `invariants:`, and a step's
`retry:` field: declaring any of them is a validation error, not a quietly-ignored field.

---

Next: [dogfood.md](dogfood.md) — actually running a workflow end to end.
