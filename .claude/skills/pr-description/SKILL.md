---
name: pr-description
description: The required description template for every GitHub pull request on dcferreira/agent-pawl. Use whenever a PR description or title is written or edited here, however the PR is opened (gh pr create, gh pr edit, the review-pr workflow, or any other route). Also bans Claude Code session / Remote Control URLs and Claude-Session trailers in PR titles, descriptions and commit messages.
---

# PR description

Every pull request on `dcferreira/agent-pawl` uses the description template in
`.github/pull_request_template.md`, however it is opened.

Template and guidelines adapted from manage-mr's `phases/describe.md` ("MR description template" and
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

The description MUST follow `.github/pull_request_template.md`, the single source of truth for the
headings (GitHub pre-fills it for PRs opened in the web UI). Read that file and fill it in; don't
keep or paste a copy of its headings anywhere else. Omit optional sections only if they genuinely
don't apply. Replace the HTML comments with actual content. With `gh pr create`, start from the
file's contents (`gh pr create --body-file` on a filled-in copy, or `--template
pull_request_template.md` in the editor flow).

## Writing guidelines

- **Be concise and scannable**: bullet points over paragraphs.
- **Explain the "why"**: don't just restate what the diff shows.
- **No people or process in the reasoning**: State the technical reasoning: the problem or gap and
  why this is the right fix. Don't mention who asked or approved the change, or any team roles or
  process; write as a contributor to the project. The same goes for commit messages and changelog
  fragments.
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
