// Package boomi is a Go client for the Boomi Platform REST API: transport,
// auth, rate limiting, retry, and pagination in one importable module.
// Component XML and other bodies pass through as opaque bytes — this
// package never parses, rewrites, or round-trips them.
//
// # Pacing
//
// The platform allows roughly 10 requests per second per account and
// answers 503 above that. The allowance is shared with the customer's
// production traffic and anyone in the Boomi UI, so the SDK paces at 8 by
// default and New rejects any configured RPS above 10. The limiter is
// process-wide and belongs to the account, not the client: constructing a
// fresh Client never resets pacing. Writes are serialised; reads run
// concurrently inside the paced budget (Config.ReadConcurrency, default 4).
//
// # Retry
//
// Retryable failures — 429, 503, other 5xx, and network timeouts — are
// retried with full-jitter exponential backoff: base 500ms, multiplier 2,
// cap 8s, honouring a server Retry-After up to 60s, for at most 3 total
// wire sends (the initial attempt plus 2 retries). Then the call fails.
//
// 401 and 403 are NEVER retried, at any level. Backoff makes a rejection
// slower, not safer, and repeatedly retrying a rejected credential locks
// the account. After two auth rejections in a process the auth circuit
// opens and further calls fail locally with ErrCircuitOpen instead of
// reaching the wire.
//
// # Pagination
//
// Paginated queries complete or they fail. The SDK reads numberOfResults
// from the first page and checks the collected count before returning;
// a broken queryMore chain yields an error wrapping ErrTruncated, never
// partial results with a nil error.
//
// # Observation
//
// The SDK never writes to stdout or stderr. Progress — requests, pacing
// waits, throttled retries, pages — is delivered to the progress.Observer
// in Config, which is the SDK's only output channel. The default is
// progress.Nop.
//
// # Errors and exit codes
//
// Errors carry a Kind, retrievable with KindOf, that maps onto the ata
// CLI exit-code contract: KindValidation→2, KindAuth→3, KindConflict→4,
// KindTransport→5. The SDK itself never calls os.Exit; callers map Kinds
// to codes.
package boomi
