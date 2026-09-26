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
  adding one).
- **`check-no-version-bump.sh`** fails a PR that touches `CHANGELOG.md`, adds/modifies/deletes a
  `.changes/v*.md` release-notes file, or changes `.claude-plugin/plugin.json`'s `version` field —
  those only ever change on a `release/v*` branch. Editing or deleting your own
  `.changes/unreleased/` fragment (e.g. fixing a typo) is fine; only the release artefacts
  themselves are locked.

`ci.yml`'s lint job also runs `scripts/release/check-version-consistency.sh` on every PR and on
`main`, which fails unless `plugin.json`'s version matches the highest version recorded under
`.changes/`. It's a cheap sanity check that the two never quietly drift, independent of the PR-diff
checks above.

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
   set it explicitly (`1.2.3`, `minor`, `major`, `patch`).
2. The workflow batches every `.changes/unreleased/` fragment into a new `.changes/vX.Y.Z.md`,
   regenerates `CHANGELOG.md`, bumps `.claude-plugin/plugin.json`'s version, and opens a
   `release/vX.Y.Z` PR (via a GitHub App token — see below).
3. Review the PR like any other: read the generated release notes, fix any fragment wording, and
   confirm the version bump looks right (edit the release PR directly rather than re-running the
   workflow, unless you want to redo it from scratch).
4. Merge it.
5. The merge (a push to `main` that touches `CHANGELOG.md`) triggers `.github/workflows/release.yml`,
   which reads the version from `.changes/`, tags `vX.Y.Z`, and runs goreleaser with that version's
   release notes in the same job — publishing the GitHub Release and its binaries.

## One-time setup: the GitHub App

`release-pr.yml` opens its PR with a GitHub App installation token rather than the default
`GITHUB_TOKEN`, because a PR opened with the default token triggers no further workflow runs
(GitHub's anti-recursion rule) — CI would never run on the release PR. An App token doesn't have
that restriction.

Once, for this repo:

1. Create a GitHub App (can be repo-scoped) with permissions **Contents: read & write** and **Pull
   requests: read & write**.
2. Install it on this repository.
3. Add its App ID and a generated private key as repo secrets `RELEASE_APP_ID` and
   `RELEASE_APP_PRIVATE_KEY`.

## Recovery

If `release.yml` fails partway (e.g. goreleaser flaked after the tag was already pushed), re-run it
from **Actions → Release → Run workflow** (`workflow_dispatch`, `main` selected). It's idempotent
across all three states a re-run can find:

- tag and GitHub Release both already exist: nothing to do.
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
