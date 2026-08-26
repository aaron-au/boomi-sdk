package boomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aaron-au/boomi-sdk/pace"
	"github.com/aaron-au/boomi-sdk/progress"
)

// Client talks to the Boomi Platform API for one account. Construct it
// with New. A Client is safe for concurrent use.
//
// Pacing state is not stored per Client: the limiter and breaker are
// process-wide, keyed by account (pace.ForAccount / pace.BreakerFor), so a
// fresh Client never resets pacing.
type Client struct {
	cfg        Config // validated, defaults applied
	httpClient *http.Client
	observer   progress.Observer
	userAgent  string
	authUser   string // "BOOMI_TOKEN." + cfg.Username
	authPass   string

	// limiter and breaker are attached lazily on first use via paceKey();
	// nil until then. They are looked up, not owned: the same account in
	// two Clients shares one budget and one circuit.
	paceOnce sync.Once
	limiter  *pace.Limiter
	breaker  *pace.Breaker
}

// Observer returns the progress observer this client emits events to. It
// is never nil: an unset Config.Observer defaults to progress.Nop. Layers
// above transport (pagination in objects) use it to emit their own events
// through the same channel.
func (c *Client) Observer() progress.Observer {
	return c.observer
}

// paceKey returns the process-wide registry key for this client's account.
// Host is lowercased per the pace.Key contract.
func (c *Client) paceKey() pace.Key {
	return pace.Key{
		Host:            strings.ToLower(c.cfg.Host),
		AccountID:       c.cfg.AccountID,
		OverrideAccount: c.cfg.OverrideAccount,
	}
}

// attachPace looks up the process-wide limiter and breaker for this
// client's account, exactly once per Client. Configure is first-wins and
// tighten-only inside pace, so racing Clients on the same account cannot
// loosen each other's budget.
func (c *Client) attachPace() {
	c.paceOnce.Do(func() {
		k := c.paceKey()
		c.limiter = pace.ForAccount(k)
		c.breaker = pace.BreakerFor(k)
		c.limiter.Configure(c.cfg.RPS, c.cfg.ReadConcurrency)
	})
}

// sleepCtx pauses for d or until ctx is done, whichever comes first.
// Package-level var so tests could stub it; production keeps the default.
//
//nolint:gochecknoglobals // test seam: tests stub the sleep, production never does
var sleepCtx = func(ctx context.Context, d time.Duration) error {
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

// Do sends one request through pacing, auth-circuit, and retry handling
// and returns the raw response. The caller must close Response.Body.
//
//   - Breaker.Allow is consulted first; an open circuit fails locally
//     with an error wrapping ErrCircuitOpen (and pace.ErrOpen) before
//     anything hits the wire.
//   - Limiter.Acquire paces the send; the wait is reported to the
//     observer as a PacedEvent, never printed. The slot is held for the
//     whole retry loop, so retries never widen concurrency.
//   - 429/503/other 5xx and network failures retry per Config.Retry with
//     Retry-After honoured, emitting a ThrottledEvent before each pause;
//     at most Config.Retry.MaxAttempts requests ever reach the wire.
//   - 401/403 are never retried: the body is captured, the breaker
//     records the auth failure, and the returned error wraps ErrAuth.
//   - Request bodies stream; they are never buffered. A body that
//     implements io.Seeker is rewound and re-sent on retry. A non-nil
//     body that cannot seek is sent exactly once: if that single send
//     fails retryably, the failure is returned without retrying.
//   - ctx is respected everywhere, including mid-backoff: cancellation
//     during a retry pause returns ctx.Err().
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	c.attachPace()

	// A locked account is not recovered by trying harder: once the auth
	// circuit is open, fail locally with zero network activity.
	if err := c.breaker.Allow(); err != nil {
		return nil, fmt.Errorf("%w (%w)", ErrCircuitOpen, err)
	}

	urlStr, urlErr := c.url(req)
	if urlErr != nil {
		return nil, urlErr
	}

	path := strings.Join(req.Path, "/")

	release, waited, acquireErr := c.limiter.Acquire(ctx, req.Class)
	if acquireErr != nil {
		return nil, acquireErr
	}
	defer release()

	if waited > 0 {
		c.observer.OnPaced(progress.PacedEvent{Waited: waited, RPS: c.cfg.RPS})
	}

	// Body strategy across retries: nil is trivially resendable; a seeker
	// is rewound to its starting offset per attempt; anything else is a
	// one-shot stream, so retryable failures on it are not retried.
	rewind, rewindErr := rewinder(req, path)
	if rewindErr != nil {
		return nil, rewindErr
	}

	return c.retryLoop(ctx, req, urlStr, path, rewind)
}

// rewinder returns the body-rewind strategy for req: nil for a nil body
// or one that cannot seek, otherwise a function that returns the body to
// its starting offset for the next attempt.
func rewinder(req Request, path string) (func() error, error) {
	s, ok := req.Body.(io.Seeker)
	if !ok {
		return nil, nil //nolint:nilnil // nil rewind is the documented "body cannot be re-sent" state, not an error
	}

	start, seekErr := s.Seek(0, io.SeekCurrent)
	if seekErr != nil {
		return nil, fmt.Errorf("boomi: %s %s: reading body offset: %w", req.Method, path, seekErr)
	}

	return func() error {
		_, rewindErr := s.Seek(start, io.SeekStart)
		return rewindErr //nolint:wrapcheck // every caller wraps rewind's error with the request context
	}, nil
}

// retryLoop drives the send/evaluate/backoff cycle for one request whose
// limiter slot is already held. rewind is nil when the body cannot be
// re-sent.
func (c *Client) retryLoop(
	ctx context.Context,
	req Request,
	urlStr, path string,
	rewind func() error,
) (*Response, error) {
	bodyResendable := req.Body == nil || rewind != nil

	for attempt := 1; ; attempt++ {
		if attempt > 1 && rewind != nil {
			if err := rewind(); err != nil {
				return nil, fmt.Errorf("boomi: %s %s: rewinding body for retry: %w", req.Method, path, err)
			}
		}

		hreq, buildErr := c.buildRequest(ctx, req, urlStr, path, rewind)
		if buildErr != nil {
			return nil, buildErr
		}

		c.observer.OnRequest(progress.RequestEvent{
			Method:  req.Method,
			Path:    path,
			Attempt: attempt,
			Write:   req.Class == pace.Write,
		})

		// The body's fate is decided inside evalAttempt: streamed to the
		// caller on success, read and closed by newAPIError on failure.
		hresp, doErr := c.httpClient.Do(hreq) //nolint:bodyclose // ownership transfers to evalAttempt (see above)

		resp, wait, err := c.evalAttempt(ctx, req, path, hresp, doErr, bodyResendable, attempt)
		if err != nil {
			return nil, err
		}

		if resp != nil {
			return resp, nil
		}

		if sleepErr := sleepCtx(ctx, wait); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

// buildRequest constructs the per-attempt *http.Request: context, auth,
// User-Agent, content negotiation, and a GetBody hook when the body can
// be rewound (so the transport's own re-send paths work too).
func (c *Client) buildRequest(
	ctx context.Context,
	req Request,
	urlStr, path string,
	rewind func() error,
) (*http.Request, error) {
	hreq, err := http.NewRequestWithContext(ctx, req.Method, urlStr, req.Body)
	if err != nil {
		return nil, fmt.Errorf("boomi: %s %s: %w", req.Method, path, err)
	}

	if rewind != nil && hreq.GetBody == nil {
		body := req.Body
		hreq.GetBody = func() (io.ReadCloser, error) {
			if rewindErr := rewind(); rewindErr != nil {
				return nil, rewindErr
			}

			return io.NopCloser(body), nil
		}
	}

	hreq.SetBasicAuth(c.authUser, c.authPass)
	hreq.Header.Set("User-Agent", c.userAgent)

	if req.Body != nil && req.ContentType != "" {
		hreq.Header.Set("Content-Type", req.ContentType)
	}

	if req.Accept != "" {
		hreq.Header.Set("Accept", req.Accept)
	}

	return hreq, nil
}

// evalAttempt classifies one wire attempt. A non-nil *Response is
// success; a non-nil error fails the call now; otherwise the attempt is
// retryable after the returned wait (the throttle event has already been
// emitted).
func (c *Client) evalAttempt(
	ctx context.Context,
	req Request,
	path string,
	resp *http.Response,
	doErr error,
	bodyResendable bool,
	attempt int,
) (*Response, time.Duration, error) {
	if doErr != nil {
		wait, err := c.evalNetworkFailure(ctx, req, path, doErr, bodyResendable, attempt)
		return nil, wait, err
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// Includes 202 (accepted, result pending) and 204 (no
		// content). The body streams to the caller, who closes it.
		return &Response{
			StatusCode: resp.StatusCode,
			Header:     resp.Header,
			Body:       resp.Body,
		}, 0, nil

	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// Never retried, at any level: repeatedly retrying a
		// rejected credential locks the account.
		apiErr := c.newAPIError(req.Method, path, resp)
		c.breaker.RecordAuthFailure()

		return nil, 0, fmt.Errorf("%w: %w", ErrAuth, apiErr)

	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		wait, err := c.evalRetryableStatus(req, path, resp, bodyResendable, attempt)
		return nil, wait, err

	default:
		// Remaining 4xx (400, 404, 409, ...) and anything else
		// non-2xx: the caller's request was wrong for the current
		// state; retrying cannot fix it.
		return nil, 0, c.newAPIError(req.Method, path, resp)
	}
}

// evalNetworkFailure decides whether a transport-level failure is
// retried: cancellation and non-resendable bodies fail immediately,
// otherwise the retry policy prices the next attempt.
func (c *Client) evalNetworkFailure(
	ctx context.Context,
	req Request,
	path string,
	doErr error,
	bodyResendable bool,
	attempt int,
) (time.Duration, error) {
	// Cancellation is the caller's decision, never retried.
	if ctx.Err() != nil {
		return 0, fmt.Errorf("boomi: %s %s: %w", req.Method, path, ctx.Err())
	}

	wrapped := fmt.Errorf("boomi: %s %s: %w", req.Method, path, doErr)
	cause := "network error"

	var netErr net.Error
	if errors.As(doErr, &netErr) && netErr.Timeout() {
		cause = "network timeout"
	}

	maxAttempts := c.cfg.Retry.MaxAttempts
	if !bodyResendable || attempt >= maxAttempts {
		return 0, wrapped
	}

	wait, werr := c.cfg.Retry.Wait(attempt, 0)
	if werr != nil {
		return 0, wrapped
	}

	c.observer.OnThrottled(progress.ThrottledEvent{
		Cause:   cause,
		Wait:    wait,
		Attempt: attempt,
		Max:     maxAttempts,
	})

	return wait, nil
}

// evalRetryableStatus prices a retry of a 429/5xx response, or fails
// with the captured *APIError when the budget or the body cannot support
// one.
func (c *Client) evalRetryableStatus(
	req Request,
	path string,
	resp *http.Response,
	bodyResendable bool,
	attempt int,
) (time.Duration, error) {
	apiErr := c.newAPIError(req.Method, path, resp)

	maxAttempts := c.cfg.Retry.MaxAttempts
	if !bodyResendable || attempt >= maxAttempts {
		return 0, apiErr
	}

	retryAfter := pace.ParseRetryAfter(resp.Header.Get("Retry-After"))

	wait, werr := c.cfg.Retry.Wait(attempt, retryAfter)
	if werr != nil {
		return 0, apiErr
	}

	c.observer.OnThrottled(progress.ThrottledEvent{
		Cause:      fmt.Sprintf("http %d", resp.StatusCode),
		StatusCode: resp.StatusCode,
		RetryAfter: retryAfter,
		Wait:       wait,
		Attempt:    attempt,
		Max:        maxAttempts,
	})

	return wait, nil
}

// newAPIError captures a failed response as an *APIError: body read up to
// maxErrorBody and closed, JSON parsed when the Content-Type says JSON and
// the bytes cooperate (parse failure leaves JSON nil, Body raw).
func (c *Client) newAPIError(method, path string, resp *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	_ = resp.Body.Close()

	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Method:     method,
		Path:       path,
		Body:       body,
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json") {
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			apiErr.JSON = m
		}
	}

	return apiErr
}
