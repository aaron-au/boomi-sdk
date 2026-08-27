package objects_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aaron-au/boomi-sdk/objects"
)

func TestExecuteSendsPropertiesForProcess(t *testing.T) {
	requireDo(t)

	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"requestId":"req-1","recordUrl":"https://x/rec"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	started, err := objects.NewExecutions(c).Execute(context.Background(), objects.ExecuteRequest{
		AtomID:    "atom-1",
		ProcessID: "proc-1",
		Properties: []objects.ExecutionPropertyValue{
			{Key: "testData", Value: "aGVsbG8="},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if started.RequestID != "req-1" {
		t.Fatalf("started = %+v, want req-1", started)
	}

	var wire map[string]any
	if unmarshalErr := json.Unmarshal([]byte(body), &wire); unmarshalErr != nil {
		t.Fatalf("body %q: %v", body, unmarshalErr)
	}

	if wire["@type"] != "ExecutionRequest" || wire["atomId"] != "atom-1" {
		t.Fatalf("wire = %v, want an ExecutionRequest for atom-1", wire)
	}

	if !strings.Contains(body, `"componentId":"proc-1"`) || !strings.Contains(body, `"key":"testData"`) {
		t.Fatalf("body = %q, want properties attached to the executed process", body)
	}
}

func TestExecuteWithoutPropertiesOmitsBlock(t *testing.T) {
	requireDo(t)

	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"requestId":"req-2"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	_, err := objects.NewExecutions(c).Execute(context.Background(), objects.ExecuteRequest{
		AtomID:    "atom-1",
		ProcessID: "proc-1",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.Contains(body, "ProcessProperties") {
		t.Fatalf("body = %q, want no ProcessProperties block", body)
	}
}

func TestAwaitRecordPollsThroughRunningStates(t *testing.T) {
	requireDo(t)

	var polls atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/ExecutionRecord/async/req-1") {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch polls.Add(1) {
		case 1:
			_, _ = fmt.Fprint(w, `{"responseStatusCode":202,"result":[]}`)
		case 2:
			_, _ = fmt.Fprint(
				w,
				`{"responseStatusCode":200,"numberOfResults":1,"result":[{"executionId":"exec-1","status":"INPROCESS"}]}`,
			)
		default:
			_, _ = fmt.Fprint(
				w,
				`{"responseStatusCode":200,"numberOfResults":1,"result":[{"executionId":"exec-1","status":"COMPLETE","processId":"proc-1"}]}`,
			)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	record, err := objects.NewExecutions(c).AwaitRecord(context.Background(), "req-1", fastAsync())
	if err != nil {
		t.Fatalf("AwaitRecord: %v", err)
	}

	if record.Status != "COMPLETE" || record.ExecutionID != "exec-1" {
		t.Fatalf("record = %+v, want COMPLETE exec-1", record)
	}

	if polls.Load() != 3 {
		t.Fatalf("polls = %d, want 3", polls.Load())
	}
}

func TestLogWaitsOutNotReadyThenStreams(t *testing.T) {
	requireDo(t)

	var (
		logPosts  atomic.Int64
		downloads atomic.Int64
	)

	var serverURL string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ProcessLog"):
			w.Header().Set("Content-Type", "application/json")

			if logPosts.Add(1) == 1 {
				// The platform's log-not-ready shape: 400 "is invalid".
				http.Error(w, `the execution id is invalid`, http.StatusBadRequest)
				return
			}

			_, _ = fmt.Fprint(w, `{"url":"`+serverURL+`/download/log.zip","statusCode":202}`)
		case strings.HasSuffix(r.URL.Path, "/download/log.zip"):
			if downloads.Add(1) == 1 {
				w.WriteHeader(http.StatusAccepted)
				return
			}

			_, _ = fmt.Fprint(w, "zip-bytes")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	serverURL = srv.URL
	c := newClient(t, srv.URL)

	stream, err := objects.NewExecutions(c).Log(context.Background(), "exec-1", fastAsync())
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	defer func() { _ = stream.Close() }()

	raw, _ := io.ReadAll(stream)
	if string(raw) != "zip-bytes" {
		t.Fatalf("log = %q, want the archive bytes", raw)
	}

	// Exactly two POSTs: the rejected one and the accepted one. Every
	// POST mints a fresh download URL whose generation starts over, so a
	// client that re-POSTs between download polls reads a
	// milliseconds-old URL each time and 202s forever. Caught live.
	if logPosts.Load() != 2 {
		t.Fatalf("posts = %d, want exactly 2 — the download URL must be polled, not re-minted", logPosts.Load())
	}

	if downloads.Load() != 2 {
		t.Fatalf("downloads = %d, want 2 (one 202, one archive)", downloads.Load())
	}
}
