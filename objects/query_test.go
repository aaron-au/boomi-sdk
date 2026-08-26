package objects_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/objects"
)

type widget struct {
	ID string `json:"id"`
}

// queryMoreCall records what the server saw on one queryMore request.
type queryMoreCall struct {
	contentType string
	accept      string
	body        string
}

func TestQueryAllThreePages(t *testing.T) {
	requireDo(t)

	var (
		mu        sync.Mutex
		firstBody string
		moreCalls []queryMoreCall
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		defer mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, "/Widget/query"):
			if r.Method != http.MethodPost {
				t.Errorf("query method = %s, want POST", r.Method)
			}

			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("query Content-Type = %q, want application/json", ct)
			}

			firstBody = string(body)

			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"numberOfResults":5,"queryToken":"tok-1","result":[{"id":"a"},{"id":"b"}]}`)
		case strings.HasSuffix(r.URL.Path, "/Widget/queryMore"):
			call := queryMoreCall{
				contentType: r.Header.Get("Content-Type"),
				accept:      r.Header.Get("Accept"),
				body:        string(body),
			}
			moreCalls = append(moreCalls, call)

			w.Header().Set("Content-Type", "application/json")

			switch call.body {
			case "tok-1":
				_, _ = fmt.Fprint(w, `{"numberOfResults":5,"queryToken":"tok-2","result":[{"id":"c"},{"id":"d"}]}`)
			case "tok-2":
				_, _ = fmt.Fprint(w, `{"numberOfResults":5,"result":[{"id":"e"}]}`)
			default:
				t.Errorf("unexpected queryMore token %q", call.body)
				http.Error(w, "bad token", http.StatusBadRequest)
			}
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	got, err := objects.QueryAll[widget](context.Background(), c, "Widget", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}

	wantIDs := []string{"a", "b", "c", "d", "e"}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d results, want %d", len(got), len(wantIDs))
	}

	for i, w := range wantIDs {
		if got[i].ID != w {
			t.Errorf("result[%d].ID = %q, want %q", i, got[i].ID, w)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if firstBody != `{"QueryFilter":{}}` {
		t.Errorf("nil filter sent body %q, want %q", firstBody, `{"QueryFilter":{}}`)
	}

	if len(moreCalls) != 2 {
		t.Fatalf("queryMore called %d times, want 2", len(moreCalls))
	}

	for i, call := range moreCalls {
		if !strings.HasPrefix(call.contentType, "text/plain") {
			t.Errorf("queryMore[%d] Content-Type = %q, want text/plain", i, call.contentType)
		}

		if !strings.HasPrefix(call.accept, "application/json") {
			t.Errorf("queryMore[%d] Accept = %q, want application/json", i, call.accept)
		}
	}

	if moreCalls[0].body != "tok-1" || moreCalls[1].body != "tok-2" {
		t.Errorf("queryMore bodies = %q, %q; want raw tokens tok-1, tok-2", moreCalls[0].body, moreCalls[1].body)
	}
}

func TestQueryAllQueryMoreFailureReturnsNothing(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/Widget/query"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"numberOfResults":4,"queryToken":"tok-1","result":[{"id":"a"},{"id":"b"}]}`)
		case strings.HasSuffix(r.URL.Path, "/Widget/queryMore"):
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	got, err := objects.QueryAll[widget](context.Background(), c, "Widget", nil)
	if err == nil {
		t.Fatal("QueryAll returned nil error after queryMore 500")
	}

	if got != nil {
		t.Fatalf("QueryAll returned %d partial results with an error; want zero", len(got))
	}
}

func TestQueryAllCountMismatchIsTruncated(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"numberOfResults":10,"result":[{"id":"a"},{"id":"b"},{"id":"c"}]}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	got, err := objects.QueryAll[widget](context.Background(), c, "Widget", nil)
	if !errors.Is(err, boomi.ErrTruncated) {
		t.Fatalf("err = %v, want errors.Is(err, boomi.ErrTruncated)", err)
	}

	if got != nil {
		t.Fatalf("QueryAll returned %d results with a truncation error; want zero", len(got))
	}
}

func TestQueryAllMissingResultKey(t *testing.T) {
	requireDo(t)

	var mu sync.Mutex

	moreCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, "/Widget/query"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"numberOfResults":0}`)
		case strings.HasSuffix(r.URL.Path, "/Widget/queryMore"):
			moreCalls++

			http.Error(w, "should not be called", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	got, err := objects.QueryAll[widget](context.Background(), c, "Widget", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("got %d results, want 0", len(got))
	}

	mu.Lock()
	defer mu.Unlock()

	if moreCalls != 0 {
		t.Errorf("queryMore called %d times on an empty first page, want 0", moreCalls)
	}
}

func TestQueryAllZeroResults(t *testing.T) {
	requireDo(t)

	var mu sync.Mutex

	moreCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, "/Widget/query"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"numberOfResults":0,"result":[]}`)
		case strings.HasSuffix(r.URL.Path, "/Widget/queryMore"):
			moreCalls++

			http.Error(w, "should not be called", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	got, err := objects.QueryAll[widget](context.Background(), c, "Widget", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("got %d results, want 0", len(got))
	}

	mu.Lock()
	defer mu.Unlock()

	if moreCalls != 0 {
		t.Errorf("queryMore called %d times on a zero-result query, want 0", moreCalls)
	}
}

func TestQueryOne(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(string(body), "present") {
			_, _ = fmt.Fprint(w, `{"numberOfResults":1,"result":[{"id":"hit"}]}`)
			return
		}

		_, _ = fmt.Fprint(w, `{"numberOfResults":0}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	filter := []byte(`{"QueryFilter":{"expression":{"operator":"EQUALS","property":"id","argument":["present"]}}}`)

	got, err := objects.QueryOne[widget](context.Background(), c, "Widget", filter)
	if err != nil {
		t.Fatalf("QueryOne: %v", err)
	}

	if got == nil || got.ID != "hit" {
		t.Fatalf("QueryOne = %+v, want &{hit}", got)
	}

	missing, err := objects.QueryOne[widget](context.Background(), c, "Widget", nil)
	if err != nil {
		t.Fatalf("QueryOne (no match): %v", err)
	}

	if missing != nil {
		t.Fatalf("QueryOne (no match) = %+v, want nil", missing)
	}
}
