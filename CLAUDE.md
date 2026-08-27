# boomi-sdk

Go SDK for the Boomi Platform API. Zero dependencies, including tests — the
empty root go.sum is deliberate.

## Rules

1. **gosec findings are actioned, never excluded.** No `#nosec` comments, no
   gosec entries in `.golangci.yml` exclusions, no narrowing the scan. If the
   finding is wrong, the code is restructured until the scanner agrees; if
   that seems impossible, stop and ask the owner.

2. **HANDOFF.md is updated after every iteration.** Assume each request
   arrives in a fresh session with no memory of the last one: HANDOFF.md is
   what guides the next steps. Read it before starting work; rewrite the
   affected sections before finishing. It records where things stand, settled
   decisions, gotchas, and what is not done — not build history (that is
   CHANGELOG.md's job).

## Working here

- The task runner lives at `./bin/task` (not on PATH). Run `./bin/task fmt`
  before `./bin/task check` — the formatter rewrites files and the diff gate
  fails on layout alone.
- `./bin/task check` is the full CI gate: fmt, vet, lint, race tests,
  gosec/govulncheck/osv-scanner/gitleaks, 5-platform build matrix. Green
  before every commit.
- Pacing registries are process-global and never reset: wire-touching tests
  need a unique AccountID each and stay serial. `.golangci.yml` documents the
  paralleltest exclusion.
- Settled design decisions (raw-by-default access, internal query builder,
  no byte-exact XML serializer, scope exclusions) are listed in HANDOFF.md.
  Do not relitigate them without asking.
