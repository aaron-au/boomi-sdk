package objects_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aaron-au/boomi-sdk/objects"
)

// componentServer records each Component request the server saw.
type componentCall struct {
	method      string
	escapedPath string
	contentType string
	accept      string
	body        []byte
}

func newComponentServer(t *testing.T, respond string) (server *httptest.Server, getCalls func() []componentCall) {
	t.Helper()

	var (
		mu    sync.Mutex
		calls []componentCall
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()

		calls = append(calls, componentCall{
			method:      r.Method,
			escapedPath: r.URL.EscapedPath(),
			contentType: r.Header.Get("Content-Type"),
			accept:      r.Header.Get("Accept"),
			body:        body,
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, respond)
	}))
	get := func() []componentCall {
		mu.Lock()
		defer mu.Unlock()

		return append([]componentCall(nil), calls...)
	}

	return srv, get
}

func readAllClose(t *testing.T, rc io.ReadCloser) string {
	t.Helper()

	defer func() { _ = rc.Close() }()

	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading response stream: %v", err)
	}

	return string(b)
}

func TestComponentsGet(t *testing.T) {
	requireDo(t)

	srv, calls := newComponentServer(t, `<Component id="abc-123"/>`)
	defer srv.Close()

	rc, err := objects.NewComponents(newClient(t, srv.URL)).Get(context.Background(), "abc-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got := readAllClose(t, rc); got != `<Component id="abc-123"/>` {
		t.Errorf("body = %q", got)
	}

	cs := calls()
	if len(cs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(cs))
	}

	if cs[0].method != http.MethodGet {
		t.Errorf("method = %s, want GET", cs[0].method)
	}

	if !strings.HasSuffix(cs[0].escapedPath, "/Component/abc-123") {
		t.Errorf("path = %q, want …/Component/abc-123", cs[0].escapedPath)
	}

	if !strings.HasPrefix(cs[0].accept, "application/xml") {
		t.Errorf("Accept = %q, want application/xml", cs[0].accept)
	}
}

func TestComponentsGetVersionTildeOnWire(t *testing.T) {
	requireDo(t)

	srv, calls := newComponentServer(t, `<x/>`)
	defer srv.Close()

	rc, err := objects.NewComponents(newClient(t, srv.URL)).GetVersion(context.Background(), "abc", 5)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}

	readAllClose(t, rc)

	cs := calls()
	if len(cs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(cs))
	}

	if !strings.HasSuffix(cs[0].escapedPath, "/Component/abc~5") {
		t.Errorf("escaped path = %q, want literal unescaped ~5 suffix", cs[0].escapedPath)
	}
}

func TestComponentsGetOnBranchTildeOnWire(t *testing.T) {
	requireDo(t)

	srv, calls := newComponentServer(t, `<x/>`)
	defer srv.Close()

	rc, err := objects.NewComponents(newClient(t, srv.URL)).GetOnBranch(context.Background(), "abc", "QjoABC")
	if err != nil {
		t.Fatalf("GetOnBranch: %v", err)
	}

	readAllClose(t, rc)

	cs := calls()
	if len(cs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(cs))
	}

	if !strings.HasSuffix(cs[0].escapedPath, "/Component/abc~QjoABC") {
		t.Errorf("escaped path = %q, want literal unescaped ~QjoABC suffix", cs[0].escapedPath)
	}
}

// Validation failures return before any wire activity, so these run even
// while Client.Do is a stub.
func TestComponentsValidation(t *testing.T) {
	c := objects.NewComponents(newClient(t, "http://invalid.test"))
	ctx := context.Background()

	if _, err := c.Get(ctx, ""); err == nil {
		t.Error("Get with empty id: want error")
	}

	if _, err := c.Get(ctx, "a~b"); err == nil {
		t.Error("Get with '~' in id: want error")
	}

	if _, err := c.GetVersion(ctx, "abc", 0); err == nil {
		t.Error("GetVersion 0: want error")
	}

	if _, err := c.GetVersion(ctx, "abc", -1); err == nil {
		t.Error("GetVersion -1: want error")
	}

	if _, err := c.GetOnBranch(ctx, "abc", "XyzABC"); err == nil {
		t.Error("GetOnBranch with non-Qjo branch id: want error")
	}

	if _, err := c.GetOnBranch(ctx, "abc", ""); err == nil {
		t.Error("GetOnBranch with empty branch id: want error")
	}

	if _, err := c.Update(ctx, "a~b", strings.NewReader("<x/>")); err == nil {
		t.Error("Update with '~' in id: want error")
	}
}

func TestComponentsCreate(t *testing.T) {
	requireDo(t)

	srv, calls := newComponentServer(t, `<Component id="new"/>`)
	defer srv.Close()

	// Bytes with quotes, unicode, and no trailing newline: must arrive
	// byte-identical.
	xml := []byte("<Component name=\"café &amp; co\">\n  <object/>\n</Component>")

	rc, err := objects.NewComponents(newClient(t, srv.URL)).Create(context.Background(), bytes.NewReader(xml))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := readAllClose(t, rc); got != `<Component id="new"/>` {
		t.Errorf("response body = %q", got)
	}

	cs := calls()
	if len(cs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(cs))
	}

	if cs[0].method != http.MethodPost {
		t.Errorf("method = %s, want POST", cs[0].method)
	}

	if !strings.HasSuffix(cs[0].escapedPath, "/Component") {
		t.Errorf("path = %q, want …/Component", cs[0].escapedPath)
	}

	if !strings.HasPrefix(cs[0].contentType, "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", cs[0].contentType)
	}

	if !bytes.Equal(cs[0].body, xml) {
		t.Errorf("body not byte-identical:\ngot  %q\nwant %q", cs[0].body, xml)
	}
}

func TestComponentsUpdate(t *testing.T) {
	requireDo(t)

	srv, calls := newComponentServer(t, `<Component id="abc" version="7"/>`)
	defer srv.Close()

	xml := []byte(`<Component id="abc"><object/></Component>`)

	rc, err := objects.NewComponents(newClient(t, srv.URL)).Update(context.Background(), "abc", bytes.NewReader(xml))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	readAllClose(t, rc)

	cs := calls()
	if len(cs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(cs))
	}

	if cs[0].method != http.MethodPost {
		t.Errorf("method = %s, want POST", cs[0].method)
	}

	if !strings.HasSuffix(cs[0].escapedPath, "/Component/abc") {
		t.Errorf("path = %q, want …/Component/abc", cs[0].escapedPath)
	}

	if !strings.HasPrefix(cs[0].contentType, "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", cs[0].contentType)
	}

	if !strings.HasPrefix(cs[0].accept, "application/xml") {
		t.Errorf("Accept = %q, want application/xml", cs[0].accept)
	}

	if !bytes.Equal(cs[0].body, xml) {
		t.Errorf("body not byte-identical:\ngot  %q\nwant %q", cs[0].body, xml)
	}
}
