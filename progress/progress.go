// Package progress defines the SDK's only output channel.
//
// The SDK never writes to stdout or stderr. Everything a caller might want
// to surface — a request going out, a pacing pause, a throttled retry, a
// page of results landing — is delivered as an event to an Observer the
// caller supplies. A CLI can render events as newline-delimited JSON, a
// hook can stay silent, a TUI can draw a bar; none of them have to
// translate another's format.
//
// The events exist so that an agent watching a long run can answer three
// questions before it decides to kill the process: is the work moving, how
// much is left, and when does it end. A slow paced run must never look
// like a hang — every event carries enough to distinguish "waiting on
// purpose" from "stuck".
package progress

import "time"

// Observer receives progress events from the SDK. Implementations must be
// safe for concurrent use and must not block: events are delivered inline
// on the request path, so a slow observer slows the run.
//
// Use Nop when no observation is wanted.
type Observer interface {
	// OnRequest fires immediately before a request is sent on the wire.
	OnRequest(RequestEvent)
	// OnPaced fires after the rate limiter has made a request wait.
	OnPaced(PacedEvent)
	// OnThrottled fires when the platform pushed back (429/503/5xx or a
	// timeout) and the SDK is backing off before a retry.
	OnThrottled(ThrottledEvent)
	// OnPage fires after each page of a paginated query is collected.
	OnPage(PageEvent)
}

// RequestEvent describes one wire send. Attempt is 1-based; a retry of the
// same logical request fires again with Attempt incremented.
type RequestEvent struct {
	Method  string
	Path    string
	Attempt int
	// Write is true for requests classified as writes (serialised by the
	// limiter), false for reads.
	Write bool
}

// PacedEvent reports a deliberate pause imposed by the rate limiter. It is
// the "work is moving, this wait is intentional" signal: Waited is how
// long this request was held, Done and Total describe the batch when one
// is known (Total is 0 otherwise), RPS is the configured pace, and ETA
// estimates time to completion at that pace.
type PacedEvent struct {
	Waited time.Duration
	Done   int
	Total  int
	RPS    float64
	ETA    time.Duration
}

// ThrottledEvent reports platform push-back and the backoff the SDK chose.
// Cause names why ("http 429", "http 503", "timeout", ...) so a watcher
// knows the request itself was fine and waiting is correct. RetryAfter is
// the server-provided hint (0 when absent), Wait is the pause actually
// taken, and Attempt of Max says how far through the retry budget this
// request is.
type ThrottledEvent struct {
	Cause      string
	StatusCode int
	RetryAfter time.Duration
	Wait       time.Duration
	Attempt    int
	Max        int
}

// PageEvent reports one collected page of a paginated query. Entity is the
// object being queried, Done is results collected so far, Total is the
// count the platform reported on the first page, and More is true while a
// queryMore token remains.
type PageEvent struct {
	Entity string
	Done   int
	Total  int
	More   bool
}

// Nop is an Observer that discards every event. It is the default when a
// Config supplies none.
//
//nolint:gochecknoglobals // Nop is an exported, immutable sentinel value, part of the package API
var Nop Observer = nopObserver{}

type nopObserver struct{}

func (nopObserver) OnRequest(RequestEvent)     {}
func (nopObserver) OnPaced(PacedEvent)         {}
func (nopObserver) OnThrottled(ThrottledEvent) {}
func (nopObserver) OnPage(PageEvent)           {}
