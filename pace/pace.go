// Package pace owns request pacing, retry policy, and the auth circuit
// breaker for the Boomi Platform API.
//
// The platform allows roughly 10 requests per second per account and the
// allowance is shared with the customer's production traffic, so limiters
// and breakers are process-wide and keyed by account (see Key): a fresh
// client must never reset pacing, because the budget belongs to the
// account, not to any client value.
//
// pace imports only the standard library. It emits no events and never
// prints; callers translate the durations it returns into progress events.
package pace

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrOpen is returned by Breaker.Allow once the auth circuit has opened
// (two auth rejections in this process). pace cannot import the root boomi
// package, so it returns this sentinel; the root package wraps it as
// boomi.ErrCircuitOpen. Test with errors.Is.
var ErrOpen = errors.New("pace: auth circuit open")

// ErrExhausted is returned (wrapped) by Policy.Wait when the attempt
// number has reached MaxAttempts: the retry budget is spent and the caller
// must fail rather than send again. Test with errors.Is.
var ErrExhausted = errors.New("pace: retry attempts exhausted")

// RetryAfterError is returned by Policy.Wait when the server's Retry-After
// exceeds the policy's RetryAfterCeiling. The caller fails instead of
// sleeping: an agent reads a long silence as a hang, and a killed run gets
// re-run. RetryAfter carries the server's requested wait so the caller can
// report it. Test with errors.As.
type RetryAfterError struct {
	// RetryAfter is the wait the server asked for.
	RetryAfter time.Duration
	// Ceiling is the policy's RetryAfterCeiling it exceeded.
	Ceiling time.Duration
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("pace: server asked to retry after %s, above the %s ceiling", e.RetryAfter, e.Ceiling)
}

// Clock seam. WP2's tests swap these to make timing deterministic; nothing
// outside the package sees them, so replacing them never shifts the API.
//
//nolint:gochecknoglobals // the clock seam is package-level by design: tests swap it, production never does
var (
	// now returns the current time.
	now = time.Now
	// sleepCtx pauses for d or until ctx is done, whichever comes first.
	sleepCtx = defaultSleep
	// randFloat returns a uniform value in [0,1); jitter draws from it.
	randFloat = rand.Float64
)

// defaultSleep is the production sleepCtx: a context-aware timer wait.
func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Key identifies the account a limiter or breaker belongs to. Host is
// stored lowercase so equivalent spellings share one budget. For partner
// mode, OverrideAccount distinguishes sub-accounts, which have their own
// allowances.
type Key struct {
	Host            string
	AccountID       string
	OverrideAccount string
}

// normalize returns k with Host lowercased, so case-varied spellings of
// the same host map to the same registry entry.
func normalize(k Key) Key {
	k.Host = strings.ToLower(k.Host)
	return k
}

// Class classifies a request for the limiter: writes are serialised, reads
// run concurrently inside the paced budget. It lives here rather than in
// the root package to avoid an import cycle; the root package re-exports
// it.
type Class int

const (
	// Read requests run concurrently, up to the configured read
	// concurrency, inside the paced budget.
	Read Class = iota
	// Write requests are exclusive: at most one holds the limiter at a
	// time.
	Write
)

// Defaults applied when a limiter is never configured: 8 requests per
// second against the platform's documented 10, and 4 concurrent reads.
const (
	defaultRPS             = 8
	defaultReadConcurrency = 4
)

// Process-wide registries. Entries are never removed: the budget belongs
// to the account for the life of the process.
//
//nolint:gochecknoglobals // process-wide by design: per-account pacing state must outlive any client value
var (
	limiters sync.Map // Key -> *Limiter
	breakers sync.Map // Key -> *Breaker
)

// Limiter paces requests for one account. Obtain one via ForAccount; the
// zero value is not usable.
type Limiter struct {
	// mu guards the token bucket (next, interval) and the rps
	// configuration state.
	mu       sync.Mutex
	interval time.Duration // spacing between grants; time.Second/rps
	rpsSet   bool          // an explicit rps has been configured

	// next is the earliest instant the bucket grants again. Burst is 1:
	// an idle limiter grants one request immediately, then every grant
	// pushes next to grant time + interval.
	next time.Time

	// writes serialises write requests; reads caps concurrent reads.
	writes *gate
	reads  *gate
}

func newLimiter() *Limiter {
	return &Limiter{
		interval: time.Duration(float64(time.Second) / defaultRPS),
		writes:   newGate(1),
		reads:    newGate(defaultReadConcurrency),
	}
}

// ForAccount returns the process-wide Limiter for k, creating it on first
// use. Every call with an equal Key returns the same Limiter for the life
// of the process — constructing a fresh Client must never reset pacing.
// Host is compared case-insensitively.
func ForAccount(k Key) *Limiter {
	k = normalize(k)
	if v, ok := limiters.Load(k); ok {
		return v.(*Limiter)
	}

	v, _ := limiters.LoadOrStore(k, newLimiter())

	return v.(*Limiter)
}

// Configure sets the limiter's request rate and read concurrency. Because
// the limiter is shared process-wide, configuration can only make pacing
// more conservative: the first explicit value for each knob wins, and
// later calls are applied only when they tighten it (a lower rps or a
// lower read concurrency); looser values are ignored. Non-positive values
// leave the corresponding knob untouched. A limiter never configured runs
// at the defaults: 8 requests per second, 4 concurrent reads.
func (l *Limiter) Configure(rps float64, readConcurrency int) {
	if rps > 0 {
		iv := time.Duration(float64(time.Second) / rps)

		l.mu.Lock()
		if !l.rpsSet || iv > l.interval {
			l.interval = iv
			l.rpsSet = true
		}
		l.mu.Unlock()
	}

	if readConcurrency > 0 {
		l.reads.tighten(readConcurrency)
	}
}

// Acquire blocks until the limiter grants a slot for a request of the
// given class, or ctx is done. On success it returns a release function
// the caller must invoke when the request completes, along with how long
// the request was held — the caller turns that into a progress event.
//
// Writes are exclusive: at most one write holds the limiter at a time.
// Reads run concurrently up to the configured read concurrency. Both then
// wait on the shared per-account token bucket, so every request — read or
// write — is paced by the same budget. On error nothing is held: a
// cancelled acquire releases any class slot it had taken.
func (l *Limiter) Acquire(ctx context.Context, class Class) (release func(), waited time.Duration, err error) {
	start := now()

	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, 0, ctxErr
	}

	g := l.reads
	if class == Write {
		g = l.writes
	}

	if acqErr := g.acquire(ctx); acqErr != nil {
		return nil, 0, acqErr
	}

	if turnErr := l.waitTurn(ctx); turnErr != nil {
		g.release()
		return nil, 0, turnErr
	}

	var once sync.Once

	release = func() { once.Do(g.release) }

	return release, now().Sub(start), nil
}

// waitTurn blocks until the token bucket grants, or ctx is done. The
// bucket has burst 1: grants are spaced at least one interval apart, and
// no credit accumulates while idle.
func (l *Limiter) waitTurn(ctx context.Context) error {
	for {
		l.mu.Lock()

		t := now()
		if !t.Before(l.next) {
			l.next = t.Add(l.interval)
			l.mu.Unlock()

			return nil
		}

		d := l.next.Sub(t)
		l.mu.Unlock()

		if err := sleepCtx(ctx, d); err != nil {
			return err
		}
	}
}

// gate is a context-aware counting semaphore whose limit can be tightened
// after creation but never loosened.
type gate struct {
	mu     sync.Mutex
	active int
	limit  int
	set    bool          // an explicit limit has been configured
	wake   chan struct{} // closed and replaced on every release
}

func newGate(limit int) *gate {
	return &gate{limit: limit, wake: make(chan struct{})}
}

func (g *gate) acquire(ctx context.Context) error {
	g.mu.Lock()
	for g.active >= g.limit {
		wake := g.wake
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}

		g.mu.Lock()
	}

	g.active++
	g.mu.Unlock()

	return nil
}

func (g *gate) release() {
	g.mu.Lock()
	g.active--
	close(g.wake)
	g.wake = make(chan struct{})
	g.mu.Unlock()
}

// tighten applies n as the limit if it is the first explicit value or
// lower than the current one; looser values are ignored.
func (g *gate) tighten(n int) {
	g.mu.Lock()
	if !g.set || n < g.limit {
		g.limit = n
		g.set = true
	}
	g.mu.Unlock()
}

// Policy describes retry backoff. Wait computes the pause for a given
// attempt as full jitter over an exponential curve: uniform in
// [0, min(Cap, Base×Multiplier^(attempt-1))]. A server-provided
// Retry-After is honoured instead, up to RetryAfterCeiling.
type Policy struct {
	Base              time.Duration
	Cap               time.Duration
	RetryAfterCeiling time.Duration
	Multiplier        float64
	MaxAttempts       int
}

// The standard retry policy returned by DefaultPolicy.
const (
	defaultBase              = 500 * time.Millisecond
	defaultCap               = 8 * time.Second
	defaultRetryAfterCeiling = 60 * time.Second
	defaultMultiplier        = 2
	defaultMaxAttempts       = 3
)

// DefaultPolicy returns the SDK's standard retry policy: base 500ms,
// multiplier 2, cap 8s, Retry-After honoured up to 60s, and 3 attempts in
// total on the wire (the initial send plus 2 retries).
func DefaultPolicy() Policy {
	return Policy{
		Base:              defaultBase,
		Cap:               defaultCap,
		RetryAfterCeiling: defaultRetryAfterCeiling,
		Multiplier:        defaultMultiplier,
		MaxAttempts:       defaultMaxAttempts,
	}
}

// Wait returns the pause before retry number attempt (1-based: attempt 1
// precedes the first retry). retryAfter is the server's Retry-After hint,
// zero when absent; when present it is honoured exactly — no jitter — up
// to RetryAfterCeiling. Otherwise the wait is full jitter: uniform in
// [0, computed backoff). Wait is a pure function of its inputs plus the
// package's jitter seam; it never sleeps.
//
// It returns an error wrapping ErrExhausted when attempt has reached
// MaxAttempts, and a *RetryAfterError when retryAfter exceeds the
// ceiling — the caller fails rather than sleeping long enough to look
// like a hang.
func (p Policy) Wait(attempt int, retryAfter time.Duration) (time.Duration, error) {
	if attempt >= p.MaxAttempts {
		return 0, fmt.Errorf("pace: %d of %d wire sends used: %w", attempt, p.MaxAttempts, ErrExhausted)
	}

	if retryAfter > 0 {
		if retryAfter > p.RetryAfterCeiling {
			return 0, &RetryAfterError{RetryAfter: retryAfter, Ceiling: p.RetryAfterCeiling}
		}

		return retryAfter, nil
	}

	if attempt < 1 {
		attempt = 1
	}

	backoff := float64(p.Base) * math.Pow(p.Multiplier, float64(attempt-1))
	if c := float64(p.Cap); backoff > c {
		backoff = c
	}

	return time.Duration(randFloat() * backoff), nil
}

// ParseRetryAfter parses an HTTP Retry-After header value: either
// delta-seconds ("5") or an HTTP-date, interpreted relative to the current
// time. Empty, invalid, negative, and past values all return 0, which
// Policy.Wait treats as "no hint".
func ParseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}

	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 0
		}

		return time.Duration(secs) * time.Second
	}

	if t, err := http.ParseTime(header); err == nil {
		if d := t.Sub(now()); d > 0 {
			return d
		}
	}

	return 0
}

// Breaker is the per-account auth circuit breaker. After two auth
// rejections (401/403) it opens and Allow refuses further calls locally:
// throttling costs nothing permanent, but repeatedly retrying a rejected
// credential locks the account. Obtain one via BreakerFor.
type Breaker struct {
	// failures counts auth rejections recorded in this process. At two
	// the circuit is open, permanently: a locked account is not
	// recovered by trying harder.
	failures atomic.Int64
}

// BreakerFor returns the process-wide Breaker for k, with the same
// lifetime and sharing rules as the limiter from ForAccount.
func BreakerFor(k Key) *Breaker {
	k = normalize(k)
	if v, ok := breakers.Load(k); ok {
		return v.(*Breaker)
	}

	v, _ := breakers.LoadOrStore(k, new(Breaker))

	return v.(*Breaker)
}

// breakerThreshold is the number of auth rejections that opens the
// circuit permanently.
const breakerThreshold = 2

// Allow reports whether a call may proceed. It returns nil while the
// circuit is closed and an error wrapping ErrOpen once it has opened. An
// open circuit never closes for the life of the process.
func (b *Breaker) Allow() error {
	if b.failures.Load() >= breakerThreshold {
		return fmt.Errorf("auth rejected twice in this process; run credential checks before retrying: %w", ErrOpen)
	}

	return nil
}

// RecordAuthFailure records one auth rejection. The second call in a
// process opens the circuit permanently for that account.
func (b *Breaker) RecordAuthFailure() {
	b.failures.Add(1)
}
