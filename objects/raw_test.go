package objects_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaron-au/boomi-sdk/objects"
)

func TestRawGetSpeaksXMLByDefault(t *testing.T) {
	requireDo(t)

	var accept, path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept, path = r.Header.Get("Accept"), r.URL.Path

		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `<Atom id="atom-1"/>`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	// The zero Format selects XML — the plugin's native tongue.
	stream, err := objects.NewRaw(c).Get(context.Background(), "", "Atom", "atom-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = stream.Close() }()

	raw, _ := io.ReadAll(stream)
	if string(raw) != `<Atom id="atom-1"/>` {
		t.Fatalf("body = %q, want the XML byte-for-byte", raw)
	}

	if accept != "application/xml" {
		t.Fatalf("Accept = %q, want application/xml", accept)
	}

	if !strings.HasSuffix(path, "/Atom/atom-1") {
		t.Fatalf("path = %s, want .../Atom/atom-1", path)
	}
}

func TestRawPostStreamsXMLBodyUntouched(t *testing.T) {
	requireDo(t)

	const doc = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<ExecutionRequest processId="proc-1" atomId="atom-1" xmlns="http://api.platform.boomi.com/"/>`

	var contentType, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		contentType, gotBody = r.Header.Get("Content-Type"), string(raw)

		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `<ExecutionRequest requestId="req-1"/>`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	stream, err := objects.NewRaw(c).Post(
		context.Background(), objects.FormatXML, strings.NewReader(doc), "ExecutionRequest",
	)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if gotBody != doc {
		t.Fatalf("body = %q, want the document byte-for-byte", gotBody)
	}

	if contentType != "application/xml" {
		t.Fatalf("Content-Type = %q, want application/xml", contentType)
	}
}

func TestRawQueryJSONSendsEmptyEnvelope(t *testing.T) {
	requireDo(t)

	var body, contentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body, contentType = string(raw), r.Header.Get("Content-Type")

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"numberOfResults":0,"result":[]}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	stream, err := objects.NewRaw(c).Query(context.Background(), objects.FormatJSON, "Environment", nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	_ = stream.Close()

	if body != `{"QueryFilter":{}}` || contentType != "application/json" {
		t.Fatalf("query = %q (%s), want the empty JSON envelope", body, contentType)
	}
}

func TestRawQueryXMLSendsEmptyQueryConfig(t *testing.T) {
	requireDo(t)

	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `<QueryResult numberOfResults="0"/>`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	stream, err := objects.NewRaw(c).Query(context.Background(), objects.FormatXML, "Folder", nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	_ = stream.Close()

	if !strings.Contains(body, "<QueryConfig") || !strings.Contains(body, "http://api.platform.boomi.com/") {
		t.Fatalf("query = %q, want the namespaced empty QueryConfig", body)
	}
}

func TestRawDeleteAndEmptyPath(t *testing.T) {
	requireDo(t)

	var method, path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)
	raw := objects.NewRaw(c)

	if err := raw.Delete(context.Background(), "Branch", "branch-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if method != http.MethodDelete || !strings.HasSuffix(path, "/Branch/branch-1") {
		t.Fatalf("call = %s %s, want DELETE .../Branch/branch-1", method, path)
	}

	if _, err := raw.Get(context.Background(), objects.FormatXML); err == nil {
		t.Fatal("Get with no path segments must fail before the wire")
	}
}
