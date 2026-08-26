package objects_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaron-au/boomi-sdk/objects"
)

// fastAsync polls quickly so tests finish in milliseconds.
func fastAsync() *objects.AsyncOptions {
	return &objects.AsyncOptions{
		InitialDelay: time.Millisecond,
		MaxDelay:     2 * time.Millisecond,
		MaxWait:      time.Second,
	}
}

func TestAsyncGetPollsUntilSettled(t *testing.T) {
	requireDo(t)

	var polls atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/async/ListQueues/atom-1"):
			_, _ = fmt.Fprint(w, `{"AsyncOperationTokenResult":{"token":"tok-1"}}`)
		case strings.HasSuffix(r.URL.Path, "/async/ListQueues/response/tok-1"):
			if polls.Add(1) < 3 {
				_, _ = fmt.Fprint(
					w,
					`{"responseStatusCode":202,"result":[{"@type":"AsyncOperationStatus","message":"Connecting to runtime..."}]}`,
				)

				return
			}

			_, _ = fmt.Fprint(
				w,
				`{"responseStatusCode":200,"numberOfResults":1,"result":[{"QueueRecord":[{"queueName":"q1","messagesCount":4}]}]}`,
			)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	res, err := objects.AsyncGet(context.Background(), c, "ListQueues", "atom-1", fastAsync())
	if err != nil {
		t.Fatalf("AsyncGet: %v", err)
	}

	if polls.Load() != 3 {
		t.Fatalf("polls = %d, want 3", polls.Load())
	}

	rows, err := objects.DecodeAsync[map[string]json.RawMessage](res)
	if err != nil {
		t.Fatalf("DecodeAsync: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
}

func TestAsyncGetSettledEmptyResultDoesNotPollForever(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/response/") {
			// 202 with numberOfResults present and no rows: settled, empty.
			_, _ = fmt.Fprint(w, `{"responseStatusCode":202,"numberOfResults":0,"result":[]}`)
			return
		}

		_, _ = fmt.Fprint(w, `{"asyncToken":{"token":"tok-empty"}}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	res, err := objects.AsyncGet(context.Background(), c, "PersistedProcessProperties", "atom-1", fastAsync())
	if err != nil {
		t.Fatalf("AsyncGet: %v", err)
	}

	if len(res.Result) != 0 {
		t.Fatalf("result rows = %d, want 0", len(res.Result))
	}
}

func TestAsyncGetNoTokenFails(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	_, err := objects.AsyncGet(context.Background(), c, "RuntimeProperties", "atom-1", fastAsync())
	if err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("err = %v, want a no-token failure", err)
	}
}

func TestAsyncGetMaxWaitExpires(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/response/") {
			_, _ = fmt.Fprint(
				w,
				`{"responseStatusCode":202,"result":[{"@type":"AsyncOperationStatus","message":"still working"}]}`,
			)

			return
		}

		_, _ = fmt.Fprint(w, `{"asyncToken":{"token":"tok-slow"}}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	opts := &objects.AsyncOptions{
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond,
		MaxWait:      10 * time.Millisecond,
	}

	_, err := objects.AsyncGet(context.Background(), c, "ListQueues", "atom-1", opts)
	if err == nil || !strings.Contains(err.Error(), "still") {
		t.Fatalf("err = %v, want a max-wait failure", err)
	}
}

func TestAsyncQuerySendsFilterAndUsesSessionID(t *testing.T) {
	requireDo(t)

	var queryBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/async/ListenerStatus/query"):
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			queryBody = string(buf)
			// The ListQueues-style session id shape must be accepted too.
			_, _ = fmt.Fprint(w, `{"QueueMessageResponse":{"sessionId":"sess-9"}}`)
		case strings.HasSuffix(r.URL.Path, "/async/ListenerStatus/response/sess-9"):
			_, _ = fmt.Fprint(w, `{"responseStatusCode":200,"numberOfResults":0,"result":[]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	_, err := objects.AsyncQuery(context.Background(), c, "ListenerStatus", nil, fastAsync())
	if err != nil {
		t.Fatalf("AsyncQuery: %v", err)
	}

	if queryBody != `{"QueryFilter":{}}` {
		t.Fatalf("query body = %q, want the empty filter envelope", queryBody)
	}
}
