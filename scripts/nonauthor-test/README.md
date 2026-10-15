# scripts/nonauthor-test

Manual kit for the "non-author test": a fresh Claude Code session that has never seen agent-pawl is
given only what a stranger gets (the released plugin, and the docs reachable from `README.md`),
authors a workflow, runs it to `TERMINAL ok`, and writes a friction log. Not run in CI, no tests of
its own. Reuses `scripts/e2e-install/lib.sh` (sandbox, env, abort guard, local marketplace).
Needs network, bwrap, tmux, curl, jq, unzip, zip, tar, git, go and claude. The full protocol
(observation, triage, success criteria) is in [PROTOCOL.md](PROTOCOL.md).

    REF=main bash scripts/nonauthor-test/prep.sh                      # 1. build the sandbox (snapshot of a ref)
    SOURCE=local TO=0.9.0 bash scripts/nonauthor-test/prep.sh         #    ... or of a published release
    bash scripts/nonauthor-test/launch.sh "$ROOT" <slug> --login      # 2. human logs in once, then /exit
    bash scripts/nonauthor-test/verify-isolation.sh "$ROOT"           # 3. re-check right before launch
    bash scripts/nonauthor-test/launch.sh "$ROOT" <slug>              # 4. start the tester (tmux brain-worker-<slug>)
    # 5. observe read-only: tmux capture-pane -p -t brain-worker-<slug>; read $ROOT/home/FRICTION.md
    bash scripts/nonauthor-test/audit.sh "$ROOT"                      # 6. exit 0 clean, 1 review hits, 2 no transcripts
    rm -rf "${ROOT:?}" "${ROOT:?}.host.env"                           # 7. teardown (holds sandbox-only credentials)

`prep.sh` prints `ROOT=...` and the verify result last. Environment: `SOURCE=snapshot|local`
(default `snapshot`; `live` is refused), `REF` (snapshot: branch or tag, default `main`), `TO` (local:
the release version), `E2E_TMP` (default `$HOME/sandboxes`). The sandbox is always kept.

- **snapshot** (primary): a fresh shallow clone of the GitHub repo in a staging dir outside the
  sandbox; `plugin.json` is patched there to `<version>-nonauthor.<shortsha>` (pawl's plugin/binary version
  check is plain string equality, so a prerelease suffix is accepted); goreleaser (the version pinned
  in `release.yml`) builds the stamped binaries in snapshot mode; `build-plugin-archive.sh` makes the
  zip and marketplace.json; the plugin is installed through the real `claude plugin` CLI from a
  directory marketplace. Docs come from the same commit.
- **local**: the published release's own zip and marketplace.json; docs from its tag tarball.

The tester sees `~/pawl-docs/` laid out as in the repo: `README.md`, `docs/` (all of it), `DESIGN.md`
and `design/format-spec.md`, so relative links resolve. Nothing else from the source tree. It also
gets `~/BRIEF.md`, `~/FRICTION.md` (the template) and `~/textstats` (a git repo seeded from
`seed/textstats`, with three planted problems: formatting, a vet failure, one failing test). The seed's files
are stored with a `.seed` suffix (stripped by `prep.sh` on copy) so the repo's own `gofmt`/`go vet`/staticcheck
checks do not trip over the deliberate problems.

## Scripts

- `enter.sh <ROOT> [cmd...]` - everything the tester does runs through this (interactive shell if no cmd).
- `verify-isolation.sh <ROOT>` - PASS/FAIL checks run inside the sandbox; exit 1 on any FAIL.
- `launch.sh <ROOT> <slug> [--login|--resume]` - detached tmux session; the default mode types one line
  (`claude --dangerously-skip-permissions "Read ~/BRIEF.md ..."`, no `--model`, the CLI default).
  `--resume` (`claude --continue ...`) is for environment recovery only.
- `audit.sh <ROOT>` - greps the tester's transcripts for source reads, `git clone`/`gh repo`,
  `internal/`, `cmd/pawl`, `AGENTS.md`, WebFetch/WebSearch; prints the model ids used.

## Safety model

1. Release artefacts only: the installed plugin has no source, and the docs snapshot is an explicit copy.
2. bwrap: host filesystem read-only; `/home /root /tmp /var/tmp /run /mnt /media` are empty tmpfs; ROOT is
   bound back at the same absolute path; private pid/ipc namespaces; `--clearenv` with an explicit
   environment (no TMUX, SSH_AUTH_SOCK, tokens, DBUS). Network stays on, so layer 3 matters.
3. Policy + audit: `WebFetch`/`WebSearch` denied in the sandbox `settings.json`; the brief forbids source
   reads; `audit.sh` catches the rest. `--dangerously-skip-permissions` is acceptable only because of layer 2.

`ROOT.host.env` (next to ROOT, outside it) holds the real claude binary path and real HOME so that
nothing in the visible tree names them. Do not copy credentials from a real `~/.claude`.
