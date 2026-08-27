package objects_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/objects"
)

type bulkRow struct {
	ID string `json:"id"`
}

func TestBulkGetReturnsResultsAndMisses(t *testing.T) {
	requireDo(t)

	var gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ComponentMetadata/bulk") {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)

			return
		}

		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"response":[
			{"statusCode":200,"Result":{"id":"a"}},
			{"statusCode":404},
			{"statusCode":200,"Result":{"id":"c"}}
		]}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	results, misses, err := objects.BulkGet[bulkRow](
		context.Background(),
		c,
		"ComponentMetadata",
		[]string{"a", "b", "c"},
	)
	if err != nil {
		t.Fatalf("BulkGet: %v", err)
	}

	if len(results) != 2 || results[0].ID != "a" || results[1].ID != "c" {
		t.Fatalf("results = %+v, want a and c in order", results)
	}

	if len(misses) != 1 || misses[0].ID != "b" || misses[0].StatusCode != http.StatusNotFound {
		t.Fatalf("misses = %+v, want b with 404", misses)
	}

	var req struct {
		Type    string          `json:"type"`
		Request []bulkRow       `json:"request"`
		Extra   json.RawMessage `json:"-"`
	}
	if unmarshalErr := json.Unmarshal([]byte(gotBody), &req); unmarshalErr != nil {
		t.Fatalf("request body %q: %v", gotBody, unmarshalErr)
	}

	if req.Type != "GET" || len(req.Request) != 3 {
		t.Fatalf("request = %+v, want type GET with 3 ids", req)
	}
}

func TestBulkGetCountMismatchIsTruncated(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"response":[{"statusCode":200,"Result":{"id":"a"}}]}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	_, _, err := objects.BulkGet[bulkRow](context.Background(), c, "ComponentMetadata", []string{"a", "b"})
	if !errors.Is(err, boomi.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestBulkGetEmptyIDsTouchesNothing(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("bulk call with no ids reached the wire")
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	results, misses, err := objects.BulkGet[bulkRow](context.Background(), c, "ComponentMetadata", nil)
	if err != nil {
		t.Fatalf("BulkGet: %v", err)
	}

	if len(results) != 0 || len(misses) != 0 {
		t.Fatalf("results = %v, misses = %v, want both empty", results, misses)
	}
}
