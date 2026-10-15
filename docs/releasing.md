# Releasing

This repo tracks a per-PR changelog with [changie](https://changie.dev) and cuts releases through
a bot-opened Release PR. Versions live in exactly one place at a time: `.changes/` fragments moving
through `CHANGELOG.md` into `.claude-plugin/plugin.json`'s `version` field, all bumped together by
the same workflow. Nobody edits `CHANGELOG.md`, a `.changes/v*.md` file, or `plugin.json`'s version
by hand in a normal PR — see the "Conventions and traps" section this adds to `AGENTS.md`.

## Writing a fragment

Every normal PR adds one fragment under `.changes/unreleased/`:

```
changie new --kind added --body "Short, user-facing description of the change." -i=false
```

Non-interactive form (what an agent should run — `changie new` prompts by default, which hangs
without a TTY):

```
changie new --kind <kind> --body "..." [--custom PR=<number>] -i=false
```

**`--kind` takes the kind's lowercase `key`** (`added`, `breaking`, `changed`, `deprecated`,
`removed`, `fixed`, `security` — see `.changie.yaml`), not its capitalized `label` — verified
against changie v1.26.0: `--kind Added` fails with `invalid kind: Added`, `--kind added` works.
`CHANGELOG.md` itself still renders the capitalized label as the section heading (`### Added`).

`--custom PR=<number>` is optional — link the fragment to its PR once the number exists (e.g.
after `gh pr create`), or leave it off and the changelog entry just won't carry a link.

### Which kind to pick

From `.changie.yaml`, in the order they render in `CHANGELOG.md`:

| Kind | Use for |
|---|---|
| `Breaking` | A change that breaks a workflow author's YAML, a script's exit-code assumption, or anything else someone might already depend on. |
| `Added` | A new capability: a step kind, a CLI flag, a new command. |
| `Changed` | A change in behavior that isn't breaking. |
| `Deprecated` | Something that still works but is on its way out. |
| `Removed` | Something that used to work and no longer does (and wasn't a `Breaking` change already covered by that kind). |
| `Fixed` | A bug fix. |
| `Security` | A security-relevant fix. |

Pre-1.0.0, every kind except `Fixed`/`Security` bumps the minor version on release (semver's
major-stays-0 escape hatch); `Fixed`/`Security` bump the patch version. This applies to `Breaking`
too, until the project reaches 1.0.0 — see the comment in `.changie.yaml`.

### No fragment needed

If a PR has no user-visible effect (pure refactor, internal test, CI-only tweak, docs typo),
apply the **`skip changelog`** label instead of adding a fragment (already created on this repo).
`.github/workflows/changelog.yml` enforces one or the other on every PR.

## What CI enforces

`.github/workflows/changelog.yml` runs `scripts/release/check-fragment.sh` and
`scripts/release/check-no-version-bump.sh` on every PR:

- **`check-fragment.sh`** fails a PR that adds no `.changes/unreleased/*.yaml` fragment, unless
  it's labeled `skip changelog` or is itself the Release PR (which consumes fragments rather than
  adding one). It also fails if any `.changes/unreleased/*.yaml` fragment at the PR head has a
  missing or unknown `kind:` (even with `skip changelog`): `kind:` must be the lowercase key from
  `.changie.yaml` (`added`, not `Added`), which `changie new` writes for you; a bad one breaks
  `changie batch` at release time.
- **`check-no-version-bump.sh`** fails a PR that touches `CHANGELOG.md`, adds/modifies/deletes a
  `.changes/v*.md` release-notes file, or changes `.claude-plugin/plugin.json`'s `version` field —
  those only ever change on a `release/v*` branch. Editing or deleting your own
  `.changes/unreleased/` fragment (e.g. fixing a typo) is fine; only the release artefacts
  themselves are locked.

`ci.yml`'s lint job also runs `scripts/release/check-version-consistency.sh` on every PR and on
`main`, which fails unless `plugin.json`'s version matches the highest version recorded under
`.changes/`. It's a cheap sanity check that the two never quietly drift, independent of the PR-diff
checks above. It also checks that `.claude-plugin/marketplace.json`'s `agent-pawl` entry is an
`archive` source for a released version (a `.changes/vV.md` exists) with a 64-hex sha256; that
entry's version may lag `plugin.json` while a release PR is open, until the release job commits.

Both `.changes/unreleased/*.yaml` fragments and `.claude-plugin/plugin.json` version changes are
allowed — expected, even — on a `release/v*` branch: that's what the Release PR workflow's commit
does. See `scripts/release/test-checks.sh` for the full set of cases these checks cover.

The `release/v*` exemption is keyed on the head branch name alone — a convenience, not a security
boundary. Anyone who can push a branch to this repo can already edit these workflows directly, so
the exemption isn't protecting anything a determined pusher couldn't bypass anyway.

## Cutting a release

Both `release-pr.yml` and `release.yml` are guarded to `main` (`if: github.ref ==
'refs/heads/main'`) even though they're `workflow_dispatch`-triggered: the Actions UI lets you
dispatch a workflow from any branch, and without the guard that would batch fragments/open a
release PR, or tag/publish a release, from whatever branch happened to be selected instead of
main.

1. Go to **Actions → Release PR → Run workflow**, with `main` selected as the branch. Leave the
   `version` input empty to let changie pick the bump automatically from the fragments' kinds, or
   set it explicitly (`1.2.3`, `minor`, `major`, `patch`). To cut a release candidate instead, see
   [Pre-releases](#pre-releases-release-candidates).
2. The workflow batches every `.changes/unreleased/` fragment into a new `.changes/vX.Y.Z.md`,
   regenerates `CHANGELOG.md`, bumps `.claude-plugin/plugin.json`'s version, and opens a
   `release/vX.Y.Z` PR (via a GitHub App token — see below).
3. Review the PR like any other: read the generated release notes, fix any fragment wording, and
   confirm the version bump looks right (edit the release PR directly rather than re-running the
   workflow, unless you want to redo it from scratch).
4. Merge it.
5. The merge (a push to `main` that touches `CHANGELOG.md`) triggers `.github/workflows/release.yml`,
   which reads the version from `.changes/`, tags `vX.Y.Z`, and runs goreleaser with that version's
   release notes in the same job — publishing the GitHub Release and its binaries — then builds and
   uploads the plugin assets (next section).

## Pre-releases (release candidates)

A release candidate (`v1.0.0-rc.1`) lets testers try a version before it is the one everybody gets.
It is cut through the same Release PR workflow, with one extra input.

### Cutting rc.N

**Actions → Release PR → Run workflow**, `main` selected, with `version` set to the explicit target
(`v1.0.0`) and `prerelease` set to `rc.N` (`rc.1`, `rc.2`, ...). `auto`/`major`/`minor`/`patch` are
refused here: changie cannot compute the target of an rc line from the fragments. The workflow runs
`scripts/release/batch.sh`, which:

- writes `.changes/v1.0.0-rc.N.md` with only the fragments added since the previous cut, and moves
  those fragments from `.changes/unreleased/` to `.changes/rc/` (parked, so the final release can
  still include them);
- bumps `.claude-plugin/plugin.json` to `1.0.0-rc.N` and opens a `release/v1.0.0-rc.N` PR.

Review and merge it like any release PR. Merging triggers `release.yml`, which publishes a GitHub
**prerelease**: goreleaser's `prerelease: auto` marks any tag with a semver prerelease suffix, and
GitHub never marks a prerelease "latest". So everything that follows "latest" stays on the last
stable version:

- `install.sh` and `pawl update` resolve `/releases/latest`, so they never pick the rc;
- the repo marketplace (`/plugin marketplace add dcferreira/agent-pawl`) is not pointed at it:
  `release.yml` skips the marketplace steps for a prerelease (`scripts/release/is-prerelease.sh`), and
  `update-repo-marketplace.sh` refuses a prerelease source on its own as a second guard.

The plugin zip and `marketplace.json` are still uploaded to the prerelease as release assets, the
same as for a stable release; they are just not what the repo marketplace points at.

Testers opt in by naming the version:

```
curl -fsSL https://raw.githubusercontent.com/dcferreira/agent-pawl/main/install.sh | PAWL_VERSION=v1.0.0-rc.1 sh
pawl update --version v1.0.0-rc.1   # an existing, non-plugin install
```

### Cutting the final release

Run the workflow again with `version` set to the explicit `v1.0.0` and `prerelease` **empty**. While
`.changes/rc/` holds parked fragments an explicit version is required (`auto` would be computed from
the rc, not the version it targets), and an empty `.changes/unreleased/` is fine. The workflow runs
`changie batch v1.0.0 --include rc --remove-prereleases`: a single `v1.0.0.md` and a single
`## v1.0.0` section in `CHANGELOG.md` holding every fragment from all the rcs plus any new ones;
`.changes/rc/` and the `v1.0.0-rc.*.md` files are deleted (the PR's `add-paths` covers `.changes/`, so
the deletions are committed). The rc notes remain on their GitHub prereleases.

While `.changes/rc/` holds parked fragments the workflow refuses any version other than the one the rc
line targets (the base of the latest `v*-rc.*.md`), for both a new rc and the final cut, so a hotfix
(e.g. `v0.7.1`) cannot be cut while an rc line is pending.

### Checklist for 1.0.0

- Right after 1.0.0 ships, open a one-line PR flipping `.changie.yaml`'s `breaking` kind to
  `auto: major` (post-1.0.0 a breaking change must bump the major; see the comment there). It must
  not land earlier, or it would change what `auto` computes for the pre-1.0.0 cuts.

Before cutting rc.1, and again on every release that reaches the marketplace (for example the
0.8.0 → 0.9.0 re-check), run the install/update harness: `bash scripts/e2e-install/run.sh`, or for an
rc, which is never on the live marketplace, `FROM=<previous> TO=<rc> SOURCE=local bash
scripts/e2e-install/run.sh` (`FROM` defaults to 0.7.0 and `TO` to 0.8.0, so set both). It installs, updates and
upgrades mid-run in throwaway sandboxes, each with its own `HOME` and `CLAUDE_CONFIG_DIR`, so it needs
network and the `claude` CLI but no login and never touches your real install; every scenario must pass
(see `scripts/e2e-install/README.md`). It is a manual check, not part of CI.

CI proves the rc flow end to end against a real, pinned changie (v1.26.0) in
`scripts/release/test-checks.sh`; set `PAWL_REQUIRE_CHANGIE=1` to make a missing changie a failure
rather than a skip (CI does).

## Plugin assets

Besides goreleaser's `pawl_<ver>_<os>_<arch>.tar.gz` archives and `checksums.txt`, each release
carries two assets built by `scripts/release/build-plugin-archive.sh` (run by `release.yml` right
after goreleaser, then `gh release upload ... --clobber`):

- `agent-pawl-plugin_<ver>.zip` — the plugin root: `.claude-plugin/plugin.json`, `hooks/hooks.json`,
  `skills/pawl/SKILL.md` (a real file), `bin/pawl`, `bin/pawl-hook`, and the four
  `libexec/<os>_<arch>/pawl` binaries unpacked from goreleaser's archives. The script verifies each
  archive against `checksums.txt` first and fails on a missing archive, a missing checksum line or a
  mismatch; it also fails unless `plugin.json`'s version equals the release version. Built with
  `zip -X` so the 0755 modes survive.
- `marketplace.json` — a one-plugin marketplace whose `archive` source is the zip's release URL
  pinned by its sha256. It is not addable by URL (Claude Code treats a github.com URL as a git
  repo); it records the zip's sha256 for the next step, and can be used for a version-pinned local
  install (see [install.md](install.md#migrating-from-an-earlier-install)).

After the upload of a stable release, `release.yml` commits the new zip URL and sha256 to `main`'s
`.claude-plugin/marketplace.json`, which is what `/plugin marketplace add dcferreira/agent-pawl`
installs from. It copies the source block from the release's own `marketplace.json` asset
(`scripts/release/update-repo-marketplace.sh`; idempotent, and it never downgrades an entry that
already points at a newer release) and pushes directly to `main` with the release GitHub App token.
A PR would leave each release uninstallable until a second manual merge. The App token, unlike
`GITHUB_TOKEN`, makes CI run on that commit; it can't recurse, because `release.yml` only triggers
on a `CHANGELOG.md` change. The App needs the Contents read & write it already has. If a `main`
ruleset or branch protection is ever enabled, the release App must be a bypass actor, or this push
fails. A failed push is retried by re-running the workflow: with the release already complete, the
re-run only redoes this step.

The script's tests (fake tarballs, no network) are part of `make test-release-checks`.

## One-time setup: the GitHub App

`release-pr.yml` opens its PR with a GitHub App installation token rather than the default
`GITHUB_TOKEN`, because a PR opened with the default token triggers no further workflow runs
(GitHub's anti-recursion rule) — CI would never run on the release PR. An App token doesn't have
that restriction.

Once, for this repo:

1. Create a GitHub App (can be repo-scoped) with permissions **Contents: read & write** and **Pull
   requests: read & write**.
2. Install it on this repository.
3. Add its Client ID (from the app's settings page, "About" section — not the numeric App ID) and
   a generated private key as repo secrets `RELEASE_APP_CLIENT_ID` and `RELEASE_APP_PRIVATE_KEY`.

## Recovery

If `release.yml` fails partway (e.g. goreleaser flaked after the tag was already pushed), re-run it
from **Actions → Release → Run workflow** (`workflow_dispatch`, `main` selected). It's idempotent
across all three states a re-run can find:

- tag and GitHub Release both already exist: nothing to do, unless the release lacks the plugin
  assets (a run that died between goreleaser and the upload) — then the workflow checks out the
  tag, downloads the published archives and uploads just the plugin assets. A release whose tag
  predates the plugin assets (no `scripts/release/build-plugin-archive.sh`) is left alone.
- tag exists but the release doesn't (goreleaser failed after the tag push): the tag isn't
  recreated (that would fail outright); the workflow checks out the tagged commit (it needn't be
  main's current tip any more — other PRs may have merged since) and runs goreleaser from there.
- neither exists: tag, push, then run goreleaser — the normal path, but on a manual
  `workflow_dispatch` this only proceeds if `main`'s current tip is still the commit that last
  changed `CHANGELOG.md`. If later commits have merged to `main` since the release PR, the workflow
  refuses rather than tag and release those unreleased commits under the old version — use
  **Re-run failed jobs** on the original run instead (it keeps the original commit), or dispatch
  before anything else merges.

Never push a `v*` tag by hand — `release.yml` no longer triggers on a tag push (it creates the tag
itself, from a `CHANGELOG.md`-touching push to `main`), so a hand-pushed tag wouldn't publish
anything and would just leave a stray tag with no matching `.changes/vX.Y.Z.md` for
`check-version-consistency.sh` to reconcile against.
