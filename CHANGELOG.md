# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - Unreleased

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
