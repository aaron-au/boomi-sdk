# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-08-27

### Fixed

Three wire-contract corrections from Phase 3 verification against a live
account (all caught by exercising the full write cycle: create, package,
deploy, execute, log download):

- `Executions.Log` polled a fresh download URL each iteration: every
  `POST ProcessLog` mints a new location whose generation starts over,
  so the archive could never become ready. The request is now made once
  (retried only through the 400 "is invalid" phase) and the single
  issued URL is polled through its 202s.
- `References.Of` queried `parentComponentId` alone, which the platform
  rejects with HTTP 400; it now requires and sends `parentVersion`.
- `PackagedComponent.ComponentVersion` was typed `string` from query
  responses, but the create response carries a bare number. Now a
  `FlexInt`, accepting both wire forms.

### Added

- Generic transport primitives beyond query pagination: bulk GET
  (`BulkGet`, chunked at the platform's 100-id ceiling, misses reported
  rather than dropped), the async token-poll API (`AsyncGet`,
  `AsyncQuery`, `DecodeAsync`, with `AsyncPollEvent` progress), and
  `Client.Download` for platform-generated files, restricted to URLs on
  the same site as the configured API host.
- Tier-2 deployment objects: PackagedComponent create and get,
  DeployedPackage deploy, undeploy, and query, Environment and
  EnvironmentAtomAttachment, EnvironmentExtensions get and update
  (partial by default), Folder create.
- Tier-3 objects: ExecutionRequest with record polling and ProcessLog
  download, ExecutionRecord queries, Branch create and delete,
  MergeRequest stage, execute, revert, and delete, ComponentReference,
  ComponentDiffRequest, SharedServerInformation.
- Runtime operations: Atom and AtomConnectorVersions, ProcessSchedules
  and ProcessScheduleStatus reads and writes, PersistedProcessProperties
  (read-modify-write with `Upsert`; the full-replace update is guarded),
  RuntimeProperties with single-property partial writes,
  AccountCloudAttachmentProperties, ListQueues, ListenerStatus,
  SharedWebServer with both platform spellings accepted and a
  token-redaction helper, EnvironmentMapExtensionsSummary and
  EnvironmentMapExtension, DeployedExpiredCertificate.
- Account administration: Account with licence position, AccountUserRole,
  Role, and the ConnectionLicensingReport CSV pipeline, including
  recovery from a stuck undownloaded report.
- Raw object access alongside the typed services: `objects.Raw` streams
  GET, POST, single-page query, and DELETE against any object path in
  XML or JSON (`Format`), byte-for-byte in both directions — the door
  for tooling that reads and writes platform documents rather than
  structs. The zero `Format` is XML.
- `componentxml`: the contained struct view of component XML. The
  envelope (`<bns:Component>` attributes, description, encryptedValues)
  is typed for reading and for authoring create/update documents; the
  inner `<bns:object>` — the component definition itself — is captured
  and re-emitted verbatim, never re-encoded. Raw streams remain the
  default for byte-exact work; the `encoding/xml` ban stays enforced
  everywhere else in the SDK.
- Confirmed-write guards: every call that changes a live environment or
  runtime (deploy, undeploy, schedule writes, extension writes, web
  server writes, branch and merge writes, persisted-property replace)
  refuses to act until the caller sets `Confirmed`.

## [0.1.0] - 2026-08-26

### Added

- Transport with Boomi token auth, per-account rate pacing (default 8
  requests per second against the platform's 10, process-wide limiter that
  survives client construction), retry with full-jitter exponential
  backoff honouring `Retry-After`, and an auth circuit breaker: 401/403
  are never retried, and two rejections in a process stop further calls
  locally.
- Complete-or-fail pagination: `query`/`queryMore` chains return every
  result or an error wrapping `ErrTruncated`, never partial results with a
  nil error.
- Tier-1 endpoints: Component get, create, and update, including the
  `~version` and `~branchId` forms; ComponentMetadata, Folder, and Branch
  queries.
- Progress observer interface; the SDK never writes to stdout or stderr.
- Typed errors: `Kind` classification mapping onto an exit-code contract,
  sentinels (`ErrCircuitOpen`, `ErrAuth`, `ErrTruncated`), and quirk
  predicates for platform behaviours hidden behind generic status codes.
