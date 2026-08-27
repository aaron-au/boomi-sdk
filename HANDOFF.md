# Handoff

State of `github.com/aaron-au/boomi-sdk` as of 2026-08-27. Written for the next session, which should assume it knows nothing this file doesn't say. Build history and each decision's "why" also live in `CHANGELOG.md` and the doc comments; this file is the map.

## Where things stand

`main` is at the PR #1 merge commit (`e93eea0`), tagged and pushed: `v0.1.0` (transport core) and `v0.2.0` (the full endpoint expansion plus three live-caught fixes). `task check` fully green. The repo is public state now — remote `git@github-personal:aaron-au/boomi-sdk`.

Phase 3 verification is done, against two real accounts:

- **Offline** (echincorporated workspace, `/Users/aaron/development/boomi-companion-customers/echincorporated`): all 371 real component documents across 13 types round-trip through `componentxml` with the inner `<bns:object>` byte-identical. Create-form documents legitimately carry no componentId.
- **Live read-only** (echincorporated-QX5H36): account, folders, component get + decode, bulk metadata, raw JSON query, raw XML get — all pass.
- **Live write cycle** (Atturra dev, `anatasptyltd-JCNRWO`, workspace `/Users/aaron/development/boomi-companion-customers/atturra`): folder create, componentxml-authored process create/update/GetVersion, package, deploy to the test environment, execute on the test atom, await record, log download, undeploy, cleanup. All pass after the fixes below. The Atturra sandpit for this work is `99. SANDPIT/Aaron Lees` (`RjoyNzg0OTg2`); test environment "ACP -Development" `6a2da312-3bde-46e9-993d-3b58c33878d8`, test atom `d1eef2f4-6726-441e-a7ee-ed7984a0e351`. Credentials in each workspace's `.env`.

The verification harnesses live in the session scratchpad (verify-componentxml, verify-live, verify-phase3), not in the repo.

## What Phase 3 taught (fixed in `71ae70f`, part of v0.2.0)

- `Executions.Log` re-POSTed `ProcessLog` on every poll. Each POST mints a fresh download URL whose generation starts over, so the archive was never ready — 202 forever. Now: POST once, retry only through the 400 "is invalid" acceptance phase, then poll the single issued URL. The unit test pins exactly two POSTs.
- `References.Of` must send `parentVersion` with `parentComponentId`; the platform rejects the id alone with HTTP 400. The signature takes the version now.
- `PackagedComponent.ComponentVersion` is a bare JSON number in the create response and a quoted one in queries. `FlexInt` (in `objects/deployments.go`) accepts both; `DeployedPackage` uses it too.
- Platform fact, not a bug: there is no `DELETE Component` objectType. Deleting the containing folder deletes its contents — that is the cleanup path.

## What the SDK covers

The v0.1 core: transport, auth, per-account pacing at 8 rps through a process-wide registry (platform allows ~10/s shared per account; `Config.RPS` raises to at most 10, never beyond), full-jitter retry honouring Retry-After, the auth circuit breaker, complete-or-fail pagination, tier-1 Component endpoints.

v0.2.0 added:

**Transport primitives** — `BulkGet` (100-id chunks, explicit misses, `ErrTruncated` on count mismatch), `AsyncGet`/`AsyncQuery`/`DecodeAsync` (all three platform token shapes including the ListQueues sessionId; 202 with `numberOfResults` present and no rows means settled-and-empty, not still-working), `Client.Download` (same-site URL restriction; 202/204 map to `ErrNotReady`), `progress.AsyncPollEvent`.

**Object families** (one file each in `objects/`) — deployments (PackagedComponent, DeployedPackage), environments + atom attachments, extensions, folders, executions (note `ExecutionRecord/async/{requestId}` is a direct-poll endpoint, not the `async/{entity}` token grammar), merge/branching, atoms, schedules (`Retry` stays a pointer — the platform rejects a zero-valued Retry block), runtime properties, queues, webserver (both platform spellings accepted: `user`/`users`, `generalSettings`/`cloudTennantGeneral`; ListenerStatus filters on `containerId`, `atomId` is rejected), map extensions (read only), certs (always sends `expirationBoundary` — omitting it silently means LESS_THAN 30), account/roles/licensing (ConnectionLicensingReport CSV pipeline with stuck-report recovery).

**Raw and typed access, the design that matters most** — `objects.Raw` streams untyped Get/Post/Query/Delete in XML or JSON (zero `Format` is XML on purpose), byte-for-byte both directions; this is the default mode for document tooling. `componentxml` types the envelope only; the inner `<bns:object>` is carried verbatim via innerxml and never re-encoded. The depguard rule keeps `encoding/xml` contained to `componentxml` and `internal/query`. Every write that changes a live environment or runtime requires `Confirmed`.

## Decisions settled with the owner

Do not relitigate without asking (SDK_PLAN §5):

- Apache-2.0. Zero dependencies, including tests — the empty root go.sum is deliberate, for Boomi's review.
- Query filter builder stays internal (`internal/query`); raw-filter escape hatches serve external demand.
- Component bodies are XML and opaque by default; typed objects speak JSON. Both modes supported; raw is the default.
- No byte-exact XML serializer. The platform re-serializes components on save, so byte fidelity cannot survive any write. If a surgical-edit need appears: textual attribute-edit helpers on raw bytes, not a DOM.
- Out of scope: Event Streams JWT/GraphQL, WSS probes against customer servers, acov3-migrate's `MergeWebServerUsers`.
- gosec findings are actioned, never excluded (see CLAUDE.md).

## Gotchas

- Pacing registries are process-global and never reset. Wire-touching tests need unique AccountIDs (`acctSeq` helper) and stay serial; `.golangci.yml` documents the paralleltest exclusion.
- Run `./bin/task fmt` before `./bin/task check` — the formatter rewrites files and the diff gate fails on layout alone. The `task` binary lives in `./bin`, not on PATH.
- `Client.AccountID()` is override-aware: partner mode returns the sub-account.
- Git guard hooks: commits happen on branches (never main), merges and `gh pr merge` are human actions, and `gh` needs an explicit identity per call — `GH_TOKEN=$(gh auth token --user aaron-au) gh ...` (two accounts exist: aaron-au personal, atturra-aaron work).
- The echincorporated workspace doc is stale in one spot: component `334b8ed4` was renamed to "XREF - Logging - Process Logging" after the 2026-07-10 snapshot. Re-running `/att-establish-environment` there would refresh it.

## What is not done

1. The CLI (`companion`, Repo 2 of the plan) — three commands first: `api`, `query`, `query-all`. Builds on `objects.Raw`. See the companion-cli memory/repo for its current state.
2. `EnvironmentMapExtension` update: read exists, write does not. The wire shape could now be captured safely from the Atturra dev account.
3. Nothing else is pending on the SDK itself; next work should start from the CLI or from new endpoint demand.

Reference material: the approved plan at `~/.claude/plans/this-project-is-the-linked-dolphin.md`, wire-shape source at `/Users/aaron/development/acpv3/acov3-migrate/pkg/boomi`, plugin scripts at `~/.claude/plugins/cache/boomi-companion/bc-integration/1.0.48/skills/boomi-integration/scripts/`.
