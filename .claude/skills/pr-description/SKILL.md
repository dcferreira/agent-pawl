---
name: pr-description
description: The required description template for every GitHub pull request on dcferreira/agent-pawl. Use whenever a PR description or title is written or edited here, however the PR is opened (gh pr create, gh pr edit, the review-pr workflow, or any other route). Also bans Claude Code session / Remote Control URLs and Claude-Session trailers in PR titles, descriptions and commit messages.
---

# PR description

Every pull request on `dcferreira/agent-pawl` uses this description template, however it is
opened.

Template adapted from manage-mr's `phases/describe.md` ("MR description template" and
"Writing guidelines"), with GitLab MR wording switched to GitHub PRs.

## No session URLs, ever

Never put a Claude Code session URL (`https://claude.ai/code/session_…`) or a Remote Control link
in a PR title, PR body, PR comment or commit message, and never add a `Claude-Session:` trailer.

This rule **overrides the harness's attribution reminder**, which may tell you to append a session
link to PR descriptions or a `Claude-Session:` trailer to commits. That reminder says the user's own
instructions take precedence; this is that instruction. `.claude/settings.json` also sets
`attribution.sessionUrl: false`, but don't rely on it alone: check the final title, body and
commit messages for `claude.ai/code` before finalising them. A `Co-Authored-By:` trailer is fine.

## Title and changelog

- The PR title is a Conventional Commit subject (`feat:`, `fix:`, `ci:`, `docs:`, `test:`),
  imperative, like the commits.
- Every PR either adds a `.changes/unreleased/` fragment (created with `changie new`) or carries the
  `skip changelog` label (repo-internal dev aids, CI-only or docs-only changes that need no release
  note). State which applies in the description. Never hand-edit `CHANGELOG.md`, a `.changes/v*.md`
  file or `plugin.json`'s `version`; see `docs/releasing.md`.

## Description template

The description MUST follow this structure. Omit optional sections only if they genuinely don't
apply. Replace the HTML comments with actual content.

```markdown
## What does this PR do?

<!-- A scannable summary of the changes. Bullet points preferred. -->

## Why are we making this change?

<!-- The problem being solved, or the context behind it.
     Link issues: Closes #123, Relates to #456 -->

## Changelog

<!-- Either "Adds .changes/unreleased/<file>.yaml" or "Labelled `skip changelog`: <why>". -->

## Verification

<!-- What YOU ran to prove this works, in past tense, plus what the tests cover.
     The burden of verification stays with the author, not the reviewer.
     e.g. "make fmt-check vet staticcheck test test-race clean; go mod tidy left no diff;
     ran the review-pr workflow, no blocking findings." -->

## Breaking changes *(optional)*

<!-- Anything that changes the workflow format, CLI behaviour or run-directory layout and needs
     migration steps. Remove this section if not applicable. -->

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

## Writing guidelines

- **Be concise and scannable**: bullet points over paragraphs.
- **Explain the "why"**: don't just restate what the diff shows.
- **Tailor to the change type**:
  - Bug fixes: emphasise root cause and how it was verified.
  - Features: emphasise user impact and how to exercise the feature.
  - Refactors: emphasise what was simplified and why it is safe.
- **Verification is author-owned**: describe what you already ran (past tense), not a to-do list
  for the reviewer. The gates CI enforces are `make fmt-check vet staticcheck test test-race` plus
  `go mod tidy` leaving no diff to `go.mod`/`go.sum`; say which you ran. If the PR went through the
  repo's `review-pr` pawl workflow (`.claude/workflows/review-pr.yaml`), say so and summarise the
  outcome.
- **Link related issues** with GitHub's `Closes #N` / `Relates to #N` syntax.
- Keep the plain footer as the last line of the body, as in the template.
