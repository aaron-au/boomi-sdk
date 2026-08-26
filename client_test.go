package boomi

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaron-au/boomi-sdk/pace"
	"github.com/aaron-au/boomi-sdk/progress"
)

// recordObserver captures every event for assertion. Safe for concurrent
// use, as the Observer contract requires.
type recordObserver struct {
	mu        sync.Mutex
	requests  []progress.RequestEvent
	paced     []progress.PacedEvent
	throttled []progress.ThrottledEvent
	pages     []progress.PageEvent
}

func (o *recordObserver) OnRequest(e progress.RequestEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.requests = append(o.requests, e)
}

func (o *recordObserver) OnPaced(e progress.PacedEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.paced = append(o.paced, e)
}

func (o *recordObserver) OnThrottled(e progress.ThrottledEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.throttled = append(o.throttled, e)
}

func (o *recordObserver) OnPage(e progress.PageEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.pages = append(o.pages, e)
}

func (o *recordObserver) OnAsyncPoll(progress.AsyncPollEvent) {}

func (o *recordObserver) throttledEvents() []progress.ThrottledEvent {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]progress.ThrottledEvent(nil), o.throttled...)
}

func (o *recordObserver) requestEvents() []progress.RequestEvent {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]progress.RequestEvent(nil), o.requests...)
}

// accountSeq makes account IDs unique per client construction, not just
// per test name, so repeated runs in one process (-count=2) never share
// breaker state either.
var accountSeq atomic.Int64

// testAccountID returns an account ID unique to this call. The limiter
// and breaker registries are process-global and never reset, so every
// test must isolate its pacing state under its own key.
func testAccountID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("acct-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), accountSeq.Add(1))
}

// fastPolicy keeps backoff pauses in the low milliseconds so exhaustion
// tests run in real time without dragging.
func fastPolicy() *pace.Policy {
	return &pace.Policy{
		Base:              time.Millisecond,
		Cap:               5 * time.Millisecond,
		RetryAfterCeiling: 60 * time.Second,
		Multiplier:        2,
		MaxAttempts:       3,
	}
}

// newTestClient builds a Client against the given test server with pacing
// state isolated under the test's own account ID.
func newTestClient(t *testing.T, serverURL string, obs progress.Observer) *Client {
	t.Helper()

	c, err := New(Config{
		Host:      serverURL,
		AccountID: testAccountID(t),
		Username:  "user",
		Token:     "token",
		RPS:       10,
		Observer:  obs,
		Retry:     fastPolicy(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return c
}

// nonSeeker hides any Seek method the wrapped reader might have.
type nonSeeker struct{ io.Reader }

func mustAPIError(t *testing.T, err error) *APIError {
	t.Helper()

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v (%T) does not wrap *APIError", err, err)
	}

	return apiErr
}

func TestDoRetries503ThenSucceeds(t *testing.T) {
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	obs := &recordObserver{}
	c := newTestClient(t, srv.URL, obs)

	resp, err := c.Do(context.Background(), Request{Method: http.MethodGet, Path: []string{"Component", "abc"}})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}

	if got := hits.Load(); got != 2 {
		t.Errorf("server hits = %d, want 2", got)
	}

	th := obs.throttledEvents()
	if len(th) != 1 {
		t.Fatalf("throttled events = %d, want 1: %+v", len(th), th)
	}

	e := th[0]
	if e.Cause != "http 503" {
		t.Errorf("Cause = %q, want %q", e.Cause, "http 503")
	}

	if e.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", e.StatusCode)
	}

	if e.RetryAfter != time.Second {
		t.Errorf("RetryAfter = %v, want 1s", e.RetryAfter)
	}

	if e.Wait <= 0 {
		t.Errorf("Wait = %v, want > 0", e.Wait)
	}

	if e.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", e.Attempt)
	}

	if e.Max != 3 {
		t.Errorf("Max = %d, want 3", e.Max)
	}

	reqs := obs.requestEvents()
	if len(reqs) != 2 {
		t.Fatalf("request events = %d, want 2: %+v", len(reqs), reqs)
	}

	for i, re := range reqs {
		if re.Attempt != i+1 {
			t.Errorf("request event %d: Attempt = %d, want %d", i, re.Attempt, i+1)
		}

		if re.Method != http.MethodGet || re.Path != "Component/abc" {
			t.Errorf("request event %d: %s %s, want GET Component/abc", i, re.Method, re.Path)
		}

		if re.Write {
			t.Errorf("request event %d: Write = true, want false for ClassRead", i)
		}
	}
}

func TestDo429ExhaustsMaxAttempts(t *testing.T) {
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	obs := &recordObserver{}
	c := newTestClient(t, srv.URL, obs)

	_, err := c.Do(context.Background(), Request{Method: http.MethodGet, Path: []string{"Atom", "query"}})
	if err == nil {
		t.Fatal("Do returned nil error, want 429 failure")
	}

	if got := hits.Load(); got != 3 {
		t.Errorf("server hits = %d, want exactly MaxAttempts (3)", got)
	}

	if got := KindOf(err); got != KindTransport {
		t.Errorf("KindOf = %v, want KindTransport", got)
	}

	apiErr := mustAPIError(t, err)
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("APIError.StatusCode = %d, want 429", apiErr.StatusCode)
	}
	// No Retry-After header: the hint must be reported as absent.
	for i, e := range obs.throttledEvents() {
		if e.RetryAfter != 0 {
			t.Errorf("throttled event %d: RetryAfter = %v, want 0", i, e.RetryAfter)
		}

		if e.Cause != "http 429" {
			t.Errorf("throttled event %d: Cause = %q, want %q", i, e.Cause, "http 429")
		}
	}

	if got := len(obs.throttledEvents()); got != 2 {
		t.Errorf("throttled events = %d, want 2 (one per retry)", got)
	}
}

func TestDo401SingleSend(t *testing.T) {
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad credentials")
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})

	_, err := c.Do(context.Background(), Request{Method: http.MethodGet, Path: []string{"Component", "x"}})
	if err == nil {
		t.Fatal("Do returned nil error, want auth failure")
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("server hits = %d, want 1 (401 never retried)", got)
	}

	if !errors.Is(err, ErrAuth) {
		t.Errorf("errors.Is(err, ErrAuth) = false, want true; err = %v", err)
	}

	if got := KindOf(err); got != KindAuth {
		t.Errorf("KindOf = %v, want KindAuth", got)
	}

	apiErr := mustAPIError(t, err)
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("APIError.StatusCode = %d, want 401", apiErr.StatusCode)
	}

	if !strings.Contains(string(apiErr.Body), "bad credentials") {
		t.Errorf("APIError.Body = %q, want it to contain the server body", apiErr.Body)
	}
}

func TestDoCircuitOpensAfterTwoAuthFailures(t *testing.T) {
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})
	req := Request{Method: http.MethodGet, Path: []string{"Component", "x"}}

	for i := 1; i <= 2; i++ {
		if _, err := c.Do(context.Background(), req); !errors.Is(err, ErrAuth) {
			t.Fatalf("call %d: err = %v, want ErrAuth", i, err)
		}
	}

	if got := hits.Load(); got != 2 {
		t.Fatalf("server hits after two calls = %d, want 2", got)
	}

	_, err := c.Do(context.Background(), req)
	if err == nil {
		t.Fatal("third call returned nil error, want open circuit")
	}

	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("errors.Is(err, ErrCircuitOpen) = false, want true; err = %v", err)
	}

	if !errors.Is(err, pace.ErrOpen) {
		t.Errorf("errors.Is(err, pace.ErrOpen) = false, want true; err = %v", err)
	}

	if got := KindOf(err); got != KindAuth {
		t.Errorf("KindOf = %v, want KindAuth", got)
	}

	if got := hits.Load(); got != 2 {
		t.Errorf("server hits after open circuit = %d, want 2 (zero network activity)", got)
	}
}

func TestDo202Passthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "pending")
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})

	resp, err := c.Do(
		context.Background(),
		Request{Method: http.MethodPost, Path: []string{"ExecutionRecord", "async", "id"}},
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("StatusCode = %d, want 202", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pending" {
		t.Errorf("body = %q, want %q", body, "pending")
	}
}

func TestDo400JSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"branch name is invalid","status":400}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})

	_, err := c.Do(context.Background(), Request{Method: http.MethodPost, Path: []string{"Branch"}})
	if err == nil {
		t.Fatal("Do returned nil error, want 400 failure")
	}

	if got := KindOf(err); got != KindValidation {
		t.Errorf("KindOf = %v, want KindValidation", got)
	}

	apiErr := mustAPIError(t, err)
	if apiErr.JSON == nil {
		t.Fatalf("APIError.JSON = nil, want parsed body; Body = %q", apiErr.Body)
	}

	if got := apiErr.JSON["message"]; got != "branch name is invalid" {
		t.Errorf("JSON[message] = %v, want %q", got, "branch name is invalid")
	}
}

func TestDo400XMLBodyRawPreserved(t *testing.T) {
	const xmlBody = `<error><message>bad request</message></error>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, xmlBody)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})

	_, err := c.Do(context.Background(), Request{Method: http.MethodPost, Path: []string{"Component"}})
	if err == nil {
		t.Fatal("Do returned nil error, want 400 failure")
	}

	apiErr := mustAPIError(t, err)
	if apiErr.JSON != nil {
		t.Errorf("APIError.JSON = %v, want nil for XML body", apiErr.JSON)
	}

	if string(apiErr.Body) != xmlBody {
		t.Errorf("APIError.Body = %q, want raw XML preserved", apiErr.Body)
	}

	if got := KindOf(err); got != KindValidation {
		t.Errorf("KindOf = %v, want KindValidation", got)
	}
}

func TestDoErrorBodyTruncatedAt64KiB(t *testing.T) {
	huge := bytes.Repeat([]byte("x"), maxErrorBody+4096)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(huge)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})

	_, err := c.Do(context.Background(), Request{Method: http.MethodGet, Path: []string{"Component", "x"}})
	if err == nil {
		t.Fatal("Do returned nil error, want 400 failure")
	}

	apiErr := mustAPIError(t, err)
	if len(apiErr.Body) != maxErrorBody {
		t.Errorf("len(APIError.Body) = %d, want capped at %d", len(apiErr.Body), maxErrorBody)
	}
}

func TestDoHeaders(t *testing.T) {
	var (
		mu     sync.Mutex
		header http.Header
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		header = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})

	resp, err := c.Do(context.Background(), Request{
		Method:      http.MethodPost,
		Path:        []string{"Component"},
		Body:        strings.NewReader(`{"a":1}`),
		ContentType: "application/json",
		Accept:      "application/xml",
		Class:       ClassWrite,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	_ = resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("BOOMI_TOKEN.user:token"))
	if got := header.Get("Authorization"); got != wantAuth {
		t.Errorf("Authorization = %q, want %q", got, wantAuth)
	}

	if got := header.Get("User-Agent"); got != c.userAgent || got == "" {
		t.Errorf("User-Agent = %q, want %q", got, c.userAgent)
	}

	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	if got := header.Get("Accept"); got != "application/xml" {
		t.Errorf("Accept = %q, want application/xml", got)
	}
}

func TestDoNonSeekableBodySentOnce(t *testing.T) {
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)

		_, _ = io.Copy(io.Discard, r.Body)

		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	obs := &recordObserver{}
	c := newTestClient(t, srv.URL, obs)

	_, err := c.Do(context.Background(), Request{
		Method:      http.MethodPost,
		Path:        []string{"Component"},
		Body:        nonSeeker{strings.NewReader("streamed once")},
		ContentType: "application/xml",
		Class:       ClassWrite,
	})
	if err == nil {
		t.Fatal("Do returned nil error, want 503 failure")
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("server hits = %d, want 1 (streamed body sent once)", got)
	}

	apiErr := mustAPIError(t, err)
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("APIError.StatusCode = %d, want 503", apiErr.StatusCode)
	}

	if got := len(obs.throttledEvents()); got != 0 {
		t.Errorf("throttled events = %d, want 0 (no retry, no backoff)", got)
	}
}

func TestDoSeekableBodyResentOnRetry(t *testing.T) {
	const payload = "payload-data-for-both-attempts"

	var (
		mu     sync.Mutex
		bodies []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		mu.Lock()

		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()

		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})

	resp, err := c.Do(context.Background(), Request{
		Method:      http.MethodPost,
		Path:        []string{"Component"},
		Body:        bytes.NewReader([]byte(payload)),
		ContentType: "application/xml",
		Class:       ClassWrite,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	_ = resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	if len(bodies) != 2 {
		t.Fatalf("server received %d requests, want 2", len(bodies))
	}

	for i, b := range bodies {
		if b != payload {
			t.Errorf("attempt %d body = %q, want %q (full body re-sent)", i+1, b, payload)
		}
	}
}

func TestDoContextCancelMidBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, &recordObserver{})

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.Do(ctx, Request{Method: http.MethodGet, Path: []string{"Component", "x"}})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Do returned nil error, want cancellation")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false, want true; err = %v", err)
	}

	if elapsed >= 2*time.Second {
		t.Errorf("Do took %v, want prompt return on cancel (Retry-After was 5s)", elapsed)
	}
}
