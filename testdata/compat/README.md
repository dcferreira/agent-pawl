# Compatibility fixtures

Frozen, validate-only copies of workflows as a released tag shipped them. Layout:
`testdata/compat/<major.minor>/<name>/`.

- `1.0/review-pr` — `.claude/workflows` at `v1.0.0-rc.1`
- `1.0/manage-mr` — `docs/examples/manage-mr` at `v1.0.0-rc.1`

**Never edit these files.** They are byte-for-byte copies; changing one defeats the point. A failing
fixture means a 1.x change broke a workflow that used to validate (see
[docs/compatibility.md](../../docs/compatibility.md)). Fix the engine, not the fixture.

`TestCompatFixturesValidate` (`internal/cli/compat_fixtures_test.go`) only validates them; nothing is run.
