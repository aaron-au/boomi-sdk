package objects_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/pace"
)

// acctSeq hands each test client a unique account id so the process-wide
// limiter and breaker registries never share state between tests.
var acctSeq atomic.Int64

// fastRetry disables retries so failure tests fail fast and deterministically.
func fastRetry() *pace.Policy {
	return &pace.Policy{
		Base:              time.Millisecond,
		Cap:               time.Millisecond,
		RetryAfterCeiling: time.Second,
		Multiplier:        1,
		MaxAttempts:       1,
	}
}

// newClient builds a client against host with a unique account id.
func newClient(t *testing.T, host string) *boomi.Client {
	t.Helper()

	c, err := boomi.New(boomi.Config{
		Host:      host,
		AccountID: fmt.Sprintf("objects-test-%d", acctSeq.Add(1)),
		Username:  "user",
		Token:     "token",
		RPS:       10,
		Retry:     fastRetry(),
	})
	if err != nil {
		t.Fatalf("boomi.New: %v", err)
	}

	return c
}

// doProbe caches whether the root Client.Do (and the pace stubs beneath
// it) are implemented. While WP2/WP3 are still panic stubs, tests that
// need the wire skip instead of panicking; they run for real once those
// work packages land.
var doProbe struct {
	once sync.Once
	ok   bool
}

func requireDo(t *testing.T) {
	t.Helper()
	doProbe.once.Do(func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, "{}")
		}))
		defer srv.Close()

		defer func() {
			if recover() != nil {
				doProbe.ok = false
			}
		}()

		c, err := boomi.New(boomi.Config{
			Host:      srv.URL,
			AccountID: fmt.Sprintf("objects-probe-%d", acctSeq.Add(1)),
			Username:  "user",
			Token:     "token",
			RPS:       10,
			Retry:     fastRetry(),
		})
		if err != nil {
			return
		}

		resp, err := c.Do(context.Background(), boomi.Request{
			Method: http.MethodGet,
			Path:   []string{"Ping"},
			Class:  boomi.ClassRead,
		})
		if err == nil {
			_ = resp.Body.Close()
		}

		doProbe.ok = true
	})

	if !doProbe.ok {
		t.Skip("root Client.Do (WP3) or pace (WP2) is still a panic stub; skipping wire test until they land")
	}
}
