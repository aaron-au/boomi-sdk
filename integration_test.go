// End-to-end integration tests exercising the SDK exactly as a consumer
// does: external test package, exported API only, real httptest servers,
// real clock. Unit tests elsewhere cover retry/backoff/circuit mechanics at
// the Do level, URL building, the pagination engine, and the filter
// grammar; these scenarios cover the cross-package seams.
//
// The limiter and breaker registries are process-global and never reset,
// so every scenario isolates its pacing state under a unique account ID.
package boomi_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/objects"
	"github.com/aaron-au/boomi-sdk/pace"
	"github.com/aaron-au/boomi-sdk/progress"
)

// integrationSeq makes account IDs unique per construction, not just per
// test name, so repeated runs in one process (-count=2) never share
// limiter or breaker state either.
var integrationSeq atomic.Int64

// integrationAccount returns an account ID unique to this call, keyed to
// the test for legibility in failures.
func integrationAccount(t *testing.T) string {
	t.Helper()

	name := strings.ReplaceAll(t.Name(), "/", "-")

	return "it-" + name + "-" + strconv.FormatInt(integrationSeq.Add(1), 10)
}

// newIntegrationClient builds a Client from cfg, filling in credentials and
// a unique account ID for any field left zero. Host must be set.
func newIntegrationClient(t *testing.T, cfg boomi.Config) *boomi.Client {
	t.Helper()

	if cfg.AccountID == "" {
		cfg.AccountID = integrationAccount(t)
	}

	if cfg.Username == "" {
		cfg.Username = "user"
	}

	if cfg.Token == "" {
		cfg.Token = "token"
	}

	c, err := boomi.New(cfg)
	if err != nil {
		t.Fatalf("boomi.New: %v", err)
	}

	return c
}

// eventLog records every progress event for later assertion. Safe for
// concurrent use, as the Observer contract requires.
type eventLog struct {
	mu        sync.Mutex
	requests  []progress.RequestEvent
	paced     []progress.PacedEvent
	throttled []progress.ThrottledEvent
	pages     []progress.PageEvent
}

func (l *eventLog) OnRequest(e progress.RequestEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.requests = append(l.requests, e)
}

func (l *eventLog) OnPaced(e progress.PacedEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.paced = append(l.paced, e)
}

func (l *eventLog) OnThrottled(e progress.ThrottledEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.throttled = append(l.throttled, e)
}

func (l *eventLog) OnPage(e progress.PageEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pages = append(l.pages, e)
}

func (l *eventLog) requestEvents() []progress.RequestEvent {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]progress.RequestEvent(nil), l.requests...)
}

func (l *eventLog) throttledEvents() []progress.ThrottledEvent {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]progress.ThrottledEvent(nil), l.throttled...)
}

func (l *eventLog) pageEvents() []progress.PageEvent {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]progress.PageEvent(nil), l.pages...)
}

// captureStdio swaps os.Stdout and os.Stderr for pipes around fn and
// returns everything written to either while fn ran. Tests here never run
// in parallel (the pacing registries are process-global by design), so the
// swap cannot race another test.
func captureStdio(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	origOut, origErr := os.Stdout, os.Stderr

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	os.Stdout, os.Stderr = outW, errW

	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	fn()

	os.Stdout, os.Stderr = origOut, origErr

	_ = outW.Close()
	_ = errW.Close()

	outBytes, _ := io.ReadAll(outR)
	errBytes, _ := io.ReadAll(errR)

	_ = outR.Close()
	_ = errR.Close()

	return string(outBytes), string(errBytes)
}

// writeJSON writes a JSON response body with the matching Content-Type.
func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

// Three pages of a five-row ComponentMetadata query in the platform's
// envelope shape: numberOfResults on the first page is the total for the
// whole query, and queryToken is absent on the last page.
const (
	metaPage1 = `{"numberOfResults":5,"queryToken":"tok-1",` +
		`"result":[{"componentId":"c1","name":"One"},{"componentId":"c2","name":"Two"}]}`
	metaPage2 = `{"numberOfResults":5,"queryToken":"tok-2",` +
		`"result":[{"componentId":"c3","name":"Three"},{"componentId":"c4","name":"Four"}]}`
	metaPage3 = `{"numberOfResults":5,"result":[{"componentId":"c5","name":"Five"}]}`
)

// newMetadataServer serves the three-page ComponentMetadata query above,
// routing queryMore requests by their raw token body. before, when
// non-nil, sees each request's token ("" for the first page) and may write
// its own response by returning true — the throttling scenario uses it to
// inject a 503.
func newMetadataServer(t *testing.T, before func(w http.ResponseWriter, token string) bool) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var token string

		switch {
		case strings.HasSuffix(r.URL.Path, "/ComponentMetadata/query"):
			// First page: the body is the filter, not a token.
		case strings.HasSuffix(r.URL.Path, "/ComponentMetadata/queryMore"):
			token = string(body)
		default:
			http.NotFound(w, r)

			return
		}

		if before != nil && before(w, token) {
			return
		}

		switch token {
		case "":
			writeJSON(w, metaPage1)
		case "tok-1":
			writeJSON(w, metaPage2)
		case "tok-2":
			writeJSON(w, metaPage3)
		default:
			http.Error(w, "unknown token "+token, http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

// assertFiveRows checks the collected results of the three-page query:
// complete and in order.
func assertFiveRows(t *testing.T, rows []objects.ComponentMetadata) {
	t.Helper()

	want := []string{"c1", "c2", "c3", "c4", "c5"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}

	for i, id := range want {
		if rows[i].ComponentID != id {
			t.Errorf("rows[%d].ComponentID = %q, want %q", i, rows[i].ComponentID, id)
		}
	}
}

// Scenario 1: a full paginated pull through objects.NewMetadata with a
// recording observer — request events, the page sequence, and the
// no-printing guarantee (nothing on stdout or stderr).
func TestIntegrationPaginatedPullWithObserver(t *testing.T) {
	srv := newMetadataServer(t, nil)
	obs := &eventLog{}
	c := newIntegrationClient(t, boomi.Config{Host: srv.URL, RPS: 10, Observer: obs})

	var (
		rows []objects.ComponentMetadata
		err  error
	)

	stdout, stderr := captureStdio(t, func() {
		rows, err = objects.NewMetadata(c).Query(context.Background(), nil)
	})

	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	assertFiveRows(t, rows)

	if stdout != "" {
		t.Errorf("SDK wrote to stdout: %q, want nothing", stdout)
	}

	if stderr != "" {
		t.Errorf("SDK wrote to stderr: %q, want nothing", stderr)
	}

	reqs := obs.requestEvents()
	if len(reqs) != 3 {
		t.Fatalf("request events = %d, want 3: %+v", len(reqs), reqs)
	}

	wantPaths := []string{
		"ComponentMetadata/query",
		"ComponentMetadata/queryMore",
		"ComponentMetadata/queryMore",
	}

	for i, e := range reqs {
		if e.Attempt != 1 {
			t.Errorf("request event %d: Attempt = %d, want 1 (no retries)", i, e.Attempt)
		}

		if e.Method != http.MethodPost || e.Path != wantPaths[i] {
			t.Errorf("request event %d: %s %s, want POST %s", i, e.Method, e.Path, wantPaths[i])
		}

		if e.Write {
			t.Errorf("request event %d: Write = true, want false (queries are reads)", i)
		}
	}

	pages := obs.pageEvents()
	wantPages := []progress.PageEvent{
		{Entity: "ComponentMetadata", Done: 2, Total: 5, More: true},
		{Entity: "ComponentMetadata", Done: 4, Total: 5, More: true},
		{Entity: "ComponentMetadata", Done: 5, Total: 5, More: false},
	}

	if len(pages) != len(wantPages) {
		t.Fatalf("page events = %d, want %d: %+v", len(pages), len(wantPages), pages)
	}

	for i, want := range wantPages {
		if pages[i] != want {
			t.Errorf("page event %d = %+v, want %+v", i, pages[i], want)
		}
	}
}

// Scenario 2: the platform throttles the middle page once with a 503 and
// Retry-After; the pull still completes and the push-back is reported as a
// ThrottledEvent, not printed.
func TestIntegrationThrottledPullCompletes(t *testing.T) {
	var failed atomic.Bool

	srv := newMetadataServer(t, func(w http.ResponseWriter, token string) bool {
		if token == "tok-1" && failed.CompareAndSwap(false, true) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)

			return true
		}

		return false
	})

	obs := &eventLog{}
	c := newIntegrationClient(t, boomi.Config{Host: srv.URL, RPS: 10, Observer: obs})

	rows, err := objects.NewMetadata(c).Query(context.Background(), nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	assertFiveRows(t, rows)

	if got := len(obs.requestEvents()); got != 4 {
		t.Errorf("request events = %d, want 4 (three pages plus one retry)", got)
	}

	th := obs.throttledEvents()
	if len(th) != 1 {
		t.Fatalf("throttled events = %d, want 1: %+v", len(th), th)
	}

	e := th[0]
	if e.Cause != "http 503" {
		t.Errorf("Cause = %q, want %q", e.Cause, "http 503")
	}

	if e.RetryAfter != time.Second {
		t.Errorf("RetryAfter = %v, want 1s", e.RetryAfter)
	}

	if e.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", e.Attempt)
	}

	if e.Max != 3 {
		t.Errorf("Max = %d, want 3 (default policy)", e.Max)
	}
}

// Scenario 3: two auth rejections open the account's circuit for the whole
// process — a brand-new Client with the same Config fails locally with
// ErrCircuitOpen and nothing further reaches the wire.
func TestIntegrationAuthLockoutAcrossClients(t *testing.T) {
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := boomi.Config{
		Host:      srv.URL,
		AccountID: integrationAccount(t),
		Username:  "user",
		Token:     "token",
		RPS:       10,
	}

	a, err := boomi.New(cfg)
	if err != nil {
		t.Fatalf("boomi.New (client A): %v", err)
	}

	req := boomi.Request{Method: http.MethodGet, Path: []string{"Component", "x"}}

	for i := 1; i <= 2; i++ {
		if _, doErr := a.Do(context.Background(), req); !errors.Is(doErr, boomi.ErrAuth) {
			t.Fatalf("client A call %d: err = %v, want ErrAuth", i, doErr)
		}
	}

	b, err := boomi.New(cfg)
	if err != nil {
		t.Fatalf("boomi.New (client B): %v", err)
	}

	_, doErr := b.Do(context.Background(), req)
	if doErr == nil {
		t.Fatal("client B first call returned nil error, want open circuit")
	}

	if !errors.Is(doErr, boomi.ErrCircuitOpen) {
		t.Errorf("errors.Is(err, ErrCircuitOpen) = false, want true; err = %v", doErr)
	}

	if got := hits.Load(); got != 2 {
		t.Errorf("server hits = %d, want 2 (client B never reached the wire)", got)
	}
}

// Scenario 4: a queryMore chain that ends before the reported total is a
// loud ErrTruncated failure of KindTransport, never partial results.
func TestIntegrationPaginationTruncationIsLoud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ComponentMetadata/query"):
			writeJSON(w, `{"numberOfResults":6,"queryToken":"tok-1",`+
				`"result":[{"componentId":"c1"},{"componentId":"c2"}]}`)
		case strings.HasSuffix(r.URL.Path, "/ComponentMetadata/queryMore"):
			// The chain breaks here: no token, but only 4 of 6 collected.
			writeJSON(w, `{"numberOfResults":6,"result":[{"componentId":"c3"},{"componentId":"c4"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newIntegrationClient(t, boomi.Config{Host: srv.URL, RPS: 10})

	rows, err := objects.NewMetadata(c).Query(context.Background(), nil)
	if !errors.Is(err, boomi.ErrTruncated) {
		t.Fatalf("err = %v, want errors.Is(err, ErrTruncated)", err)
	}

	if got := boomi.KindOf(err); got != boomi.KindTransport {
		t.Errorf("KindOf = %v, want KindTransport", got)
	}

	if rows != nil {
		t.Errorf("got %d partial rows with a truncation error, want none", len(rows))
	}
}

// Scenario 5: a partner client hits the /partner base path and carries
// overrideAccount as a real query parameter, URL-decoded intact.
func TestIntegrationPartnerClient(t *testing.T) {
	acct := integrationAccount(t)

	var (
		mu          sync.Mutex
		gotPath     string
		gotOverride string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		gotOverride = r.URL.Query().Get("overrideAccount")
		mu.Unlock()

		writeJSON(w, `{"numberOfResults":0,"result":[]}`)
	}))
	defer srv.Close()

	c := newIntegrationClient(t, boomi.Config{
		Host:            srv.URL,
		AccountID:       acct,
		Partner:         true,
		OverrideAccount: "sub acct",
		RPS:             10,
	})

	rows, err := objects.NewMetadata(c).Query(context.Background(), nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}

	mu.Lock()
	defer mu.Unlock()

	wantPath := "/partner/api/rest/v1/" + acct + "/ComponentMetadata/query"
	if gotPath != wantPath {
		t.Errorf("request path = %q, want %q", gotPath, wantPath)
	}

	if gotOverride != "sub acct" {
		t.Errorf("overrideAccount = %q, want %q (URL-decoded)", gotOverride, "sub acct")
	}
}

// Scenario 6: component XML is opaque bytes in both directions — Get hands
// back exactly the served bytes (adjacent empty-element forms untouched),
// and Update streams a 1MiB body to the server byte-identical.
func TestIntegrationComponentXMLRoundTrip(t *testing.T) {
	const served = `<bns:Component xmlns:bns="http://api.platform.boomi.com/" componentId="comp-1">` +
		`<bns:encryptedValues/><bns:description></bns:description>` +
		`<bns:object><process/></bns:object></bns:Component>`

	// A 1MiB update body, exactly 1<<20 bytes.
	const updateSize = 1 << 20

	head := []byte(`<bns:Component componentId="comp-1"><bns:object><data>`)
	tail := []byte(`</data></bns:object></bns:Component>`)
	update := make([]byte, 0, updateSize)
	update = append(update, head...)
	update = append(update, bytes.Repeat([]byte("x"), updateSize-len(head)-len(tail))...)
	update = append(update, tail...)

	var (
		mu         sync.Mutex
		getAccept  string
		updateCT   string
		updateBody []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/Component/comp-1") {
			http.NotFound(w, r)

			return
		}

		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			getAccept = r.Header.Get("Accept")
			mu.Unlock()

			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, served)
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)

			mu.Lock()
			updateCT = r.Header.Get("Content-Type")
			updateBody = body
			mu.Unlock()

			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, served)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	c := newIntegrationClient(t, boomi.Config{Host: srv.URL, RPS: 10})
	svc := objects.NewComponents(c)

	got, err := svc.Get(context.Background(), "comp-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	gotBytes, readErr := io.ReadAll(got)

	_ = got.Close()

	if readErr != nil {
		t.Fatalf("reading Get body: %v", readErr)
	}

	if string(gotBytes) != served {
		t.Errorf("Get body = %q, want the served bytes verbatim", gotBytes)
	}

	resp, err := svc.Update(context.Background(), "comp-1", bytes.NewReader(update))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	_, _ = io.Copy(io.Discard, resp)
	_ = resp.Close()

	mu.Lock()
	defer mu.Unlock()

	if getAccept != "application/xml" {
		t.Errorf("Get Accept = %q, want application/xml", getAccept)
	}

	if updateCT != "application/xml" {
		t.Errorf("Update Content-Type = %q, want application/xml", updateCT)
	}

	if len(updateBody) != updateSize {
		t.Fatalf("server received %d bytes, want %d", len(updateBody), updateSize)
	}

	if !bytes.Equal(updateBody, update) {
		t.Error("server received body differs from the streamed 1MiB payload")
	}
}

// Scenario 7: two Clients built from the same Config share one paced
// budget. At the default 8 RPS, four interleaved GETs must arrive at least
// an interval apart — spacing proves the budget is shared, not per client.
func TestIntegrationSharedPacingAcrossClients(t *testing.T) {
	var (
		mu    sync.Mutex
		times = make([]time.Time, 0, 4)
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()

		times = append(times, time.Now())
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := boomi.Config{
		Host:      srv.URL,
		AccountID: integrationAccount(t),
		Username:  "user",
		Token:     "token",
		// RPS left zero: the default of 8 applies, 125ms between grants.
	}

	c1, err := boomi.New(cfg)
	if err != nil {
		t.Fatalf("boomi.New (client 1): %v", err)
	}

	c2, err := boomi.New(cfg)
	if err != nil {
		t.Fatalf("boomi.New (client 2): %v", err)
	}

	req := boomi.Request{Method: http.MethodGet, Path: []string{"Component", "x"}}

	for i, c := range []*boomi.Client{c1, c2, c1, c2} {
		resp, doErr := c.Do(context.Background(), req)
		if doErr != nil {
			t.Fatalf("call %d: %v", i+1, doErr)
		}

		_ = resp.Body.Close()
	}

	mu.Lock()
	defer mu.Unlock()

	if len(times) != 4 {
		t.Fatalf("server received %d requests, want 4", len(times))
	}

	// 100ms floor leaves scheduling slack under the 125ms interval.
	const minSpacing = 100 * time.Millisecond

	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < minSpacing {
			t.Errorf("gap %d→%d = %v, want ≥ %v (shared budget across clients)", i, i+1, gap, minSpacing)
		}
	}
}

// Scenario 8: the Kind contract as a consumer sees it — the errors that
// come out of real calls classify onto the exit-code mapping.
func TestIntegrationKindContract(t *testing.T) {
	// fastPolicy keeps the exhausted-429 row's backoffs in the low
	// milliseconds; Kinds are unaffected by the pause lengths.
	fastPolicy := func() *pace.Policy {
		return &pace.Policy{
			Base:              time.Millisecond,
			Cap:               2 * time.Millisecond,
			RetryAfterCeiling: time.Second,
			Multiplier:        2,
			MaxAttempts:       3,
		}
	}

	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}
	}

	doGet := func(c *boomi.Client) error {
		resp, err := c.Do(context.Background(), boomi.Request{Method: http.MethodGet, Path: []string{"Component", "x"}})
		if err == nil {
			_ = resp.Body.Close()
		}

		return err
	}

	truncated := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"numberOfResults":3,"result":[{"componentId":"c1"}]}`)
	}

	cases := []struct {
		name    string
		handler http.HandlerFunc
		call    func(c *boomi.Client) error
		want    boomi.Kind
	}{
		{"auth 401", status(http.StatusUnauthorized), doGet, boomi.KindAuth},
		{"exhausted 429", status(http.StatusTooManyRequests), doGet, boomi.KindTransport},
		{"validation 400", status(http.StatusBadRequest), doGet, boomi.KindValidation},
		{
			"pagination truncated",
			truncated,
			func(c *boomi.Client) error {
				_, err := objects.NewMetadata(c).Query(context.Background(), nil)
				return err
			},
			boomi.KindTransport,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			c := newIntegrationClient(t, boomi.Config{Host: srv.URL, RPS: 10, Retry: fastPolicy()})

			err := tc.call(c)
			if err == nil {
				t.Fatal("call returned nil error, want a classified failure")
			}

			if got := boomi.KindOf(err); got != tc.want {
				t.Errorf("KindOf(%v) = %v, want %v", err, got, tc.want)
			}
		})
	}
}
